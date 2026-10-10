package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"

	"github.com/charmbracelet/huh"
)

type repositoryMetadata struct {
	ID        int64
	Name      string
	CreatedAt time.Time `json:"created_at"`
	Owner     struct {
		Login string
		ID    int64
	}
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var xx, yy any
	if json.Unmarshal(x, &xx) != nil || json.Unmarshal(y, &yy) != nil {
		return false
	}
	return reflect.DeepEqual(xx, yy)
}
func policyJSON(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func githubTrust(account, subject string) map[string]any {
	return map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{
		"Effect": "Allow", "Principal": map[string]string{"Federated": "arn:aws:iam::" + account + ":oidc-provider/token.actions.githubusercontent.com"},
		"Action": "sts:AssumeRoleWithWebIdentity", "Condition": map[string]any{"StringEquals": map[string]string{
			"token.actions.githubusercontent.com:aud": "sts.amazonaws.com", "token.actions.githubusercontent.com:sub": subject,
		}},
	}}}
}
func defaultSubject(m repositoryMetadata, environment string) (string, error) {
	if m.ID <= 0 || m.Owner.ID <= 0 || m.Name == "" || m.Owner.Login == "" || m.CreatedAt.IsZero() {
		return "", errors.New("GitHub repository metadata is incomplete")
	}
	repo := m.Owner.Login + "/" + m.Name
	if !m.CreatedAt.Before(time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)) {
		repo = fmt.Sprintf("%s@%d/%s@%d", m.Owner.Login, m.Owner.ID, m.Name, m.ID)
	}
	return "repo:" + repo + ":environment:" + environment, nil
}

type awsRoleSnapshot struct {
	Arn                      string
	AssumeRolePolicyDocument map[string]any
	Tags                     []struct{ Key, Value string }
}
type awsSetupPlan struct {
	Account, RoleName, Subject, Environment    string
	ProviderExists, AudienceExists, RoleExists bool
	Role                                       awsRoleSnapshot
	Trust                                      map[string]any
	Policies                                   map[string]any
	ExistingPolicies                           map[string]any
}

func inspectAWSSetup(a awsenv.API, account, name, subject, environment string, policies map[string]any) (awsSetupPlan, error) {
	p := awsSetupPlan{Environment: environment, Account: account, RoleName: name, Subject: subject, Trust: githubTrust(account, subject), Policies: policies, ExistingPolicies: map[string]any{}}
	var provider struct {
		Url          string
		ClientIDList []string
	}
	var err error
	p.ProviderExists, err = a.Call(&provider, "iam", "get-open-id-connect-provider", "--open-id-connect-provider-arn", "arn:aws:iam::"+account+":oidc-provider/token.actions.githubusercontent.com")
	if err != nil {
		return p, err
	}
	if p.ProviderExists && strings.TrimPrefix(provider.Url, "https://") != "token.actions.githubusercontent.com" {
		return p, errors.New("unexpected GitHub provider URL")
	}
	p.AudienceExists = slices.Contains(provider.ClientIDList, "sts.amazonaws.com")
	var role struct{ Role awsRoleSnapshot }
	p.RoleExists, err = a.Call(&role, "iam", "get-role", "--role-name", name)
	if err != nil {
		return p, err
	}
	p.Role = role.Role
	if p.RoleExists {
		expected := "arn:aws:iam::" + account + ":role/"
		if !strings.HasPrefix(p.Role.Arn, expected) {
			return p, errors.New("role belongs to an unexpected AWS account")
		}
		owned, sameRepository := false, false
		for _, tag := range p.Role.Tags {
			if tag.Key == "attestra-subject" && tag.Value == subject {
				sameRepository = true
			}
			if tag.Key == "attestra-setup" && tag.Value == "github-"+environment {
				owned = true
			}
		}
		if !owned || !sameRepository {
			return p, errors.New("existing role is not managed by this setup for the selected repository/environment; choose a new dedicated role name (existing role was left unchanged)")
		}
		for _, policy := range sortedPolicyNames(policies) {
			var old struct{ PolicyDocument map[string]any }
			found, e := a.Call(&old, "iam", "get-role-policy", "--role-name", name, "--policy-name", policy)
			if e != nil {
				return p, e
			}
			if found {
				p.ExistingPolicies[policy] = old.PolicyDocument
			}
		}
	}
	return p, nil
}
func sortedPolicyNames(p map[string]any) []string {
	names := make([]string, 0, len(p))
	for n := range p {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
func applyAWSSetup(a awsenv.API, p awsSetupPlan) (string, error) {
	provider := "arn:aws:iam::" + p.Account + ":oidc-provider/token.actions.githubusercontent.com"
	write := func(args ...string) error {
		found, e := a.Call(nil, args...)
		if e != nil {
			return e
		}
		if !found {
			return errors.New("AWS resource disappeared during setup; rerun")
		}
		fmt.Println("✓", strings.Join(args[:2], " "))
		return nil
	}
	if !p.ProviderExists {
		if e := write("iam", "create-open-id-connect-provider", "--url", "https://token.actions.githubusercontent.com", "--client-id-list", "sts.amazonaws.com"); e != nil {
			return "", e
		}
	} else if !p.AudienceExists {
		if e := write("iam", "add-client-id-to-open-id-connect-provider", "--open-id-connect-provider-arn", provider, "--client-id", "sts.amazonaws.com"); e != nil {
			return "", e
		}
	}
	if !p.RoleExists {
		if e := write("iam", "create-role", "--role-name", p.RoleName, "--assume-role-policy-document", policyJSON(p.Trust), "--tags", "Key=attestra-setup,Value=github-"+p.Environment, "Key=attestra-subject,Value="+p.Subject, "--max-session-duration", "3600"); e != nil {
			return "", e
		}
	} else if !sameJSON(p.Role.AssumeRolePolicyDocument, p.Trust) {
		if e := write("iam", "update-assume-role-policy", "--role-name", p.RoleName, "--policy-document", policyJSON(p.Trust)); e != nil {
			return "", e
		}
	}
	for _, name := range sortedPolicyNames(p.Policies) {
		if sameJSON(p.Policies[name], p.ExistingPolicies[name]) {
			continue
		}
		if e := write("iam", "put-role-policy", "--role-name", p.RoleName, "--policy-name", name, "--policy-document", policyJSON(p.Policies[name])); e != nil {
			return "", e
		}
	}
	// Read back identity, trust, provider audience, and each managed policy.
	verified, e := inspectAWSSetup(a, p.Account, p.RoleName, p.Subject, p.Environment, p.Policies)
	if e != nil {
		return "", e
	}
	if !verified.RoleExists || !verified.AudienceExists || !sameJSON(verified.Role.AssumeRolePolicyDocument, p.Trust) {
		return "", errors.New("AWS trust verification failed; IAM may still be propagating, rerun setup")
	}
	for n, policy := range p.Policies {
		if !sameJSON(policy, verified.ExistingPolicies[n]) {
			return "", fmt.Errorf("AWS policy %s verification failed; rerun setup", n)
		}
	}
	return verified.Role.Arn, nil
}

func setupAWSRole(g *github.Client, repo, environment string, metadata repositoryMetadata, values map[string]string, bootstrap *bootstrapWizard) error {
	if _, err := exec.LookPath("aws"); err != nil {
		return errors.New("install AWS CLI v2 and add it to PATH")
	}
	mode, profile := "sso", "attestra"
	savedMode, authKnown := bootstrap.remembered("authMode")
	if authKnown {
		mode = savedMode
	} else {
		if err := huh.NewSelect[string]().Title("AWS authentication for one-time IAM setup").Options(huh.NewOption("IAM Identity Center (SSO)", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Enter access key credentials", "keys")).Value(&mode).Run(); err != nil {
			return err
		}
	}
	o := options{Region: values["AWS_REGION"], Backend: values["PULUMI_BACKEND_URL"], Stack: values["PULUMI_STACK"]}
	if bootstrap != nil {
		o.Root = bootstrap.root
	}
	var c credentials
	var err error
	if mode != "keys" {
		if v, ok := bootstrap.remembered("profile"); ok {
			profile = v
		} else {
			if err = ui.Input("AWS profile", &profile, false, true).Run(); err != nil {
				return err
			}
		}
		o.Profile = profile
		o.Sso = mode == "sso"
		configure := false
		if !authKnown {
			if err = huh.NewConfirm().Title("Configure this AWS profile first? Choose yes for a new SSO profile.").Value(&configure).Run(); err != nil {
				return err
			}
		}
		if configure {
			args := []string{"configure", "--profile", profile}
			if o.Sso {
				args = []string{"configure", "sso", "--profile", profile}
			}
			if _, err = (&processRunner{env: loginEnvironment(os.Environ(), o.Region)}).Exec(o.Root, false, "aws", args...); err != nil {
				return err
			}
		}
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return err
		}
	} else {
		if bootstrap != nil {
			c.Access, c.Secret, c.Token = bootstrap.secrets.AWSAccess, bootstrap.secrets.AWSSecret, bootstrap.secrets.AWSToken
		}
		if c.Access == "" || c.Secret == "" {
			if err = huh.NewForm(huh.NewGroup(ui.Input("AWS access key ID", &c.Access, true, true), ui.Input("AWS secret access key", &c.Secret, true, true), ui.Input("AWS session token (temporary credentials)", &c.Token, true, false))).Run(); err != nil {
				return err
			}
		}

		c.Access = strings.TrimSpace(c.Access)
		c.Secret = strings.TrimSpace(c.Secret)
		c.Token = strings.TrimSpace(c.Token)
		if strings.HasPrefix(c.Access, "ASIA") && c.Token == "" {
			return errors.New("temporary AWS credentials require a session token")
		}
	}
	dir, err := os.MkdirTemp("", "attestra-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	empty := filepath.Join(dir, "aws-config")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env, err := prepareCloudEnvironment(os.Environ(), c, o, empty)
	if err != nil {
		return err
	}
	a := &awsenv.Client{Env: env, RenewalFailure: credentialRenewalFailure}
	var id struct{ Account, Arn string }
	found, err := a.Call(&id, "sts", "get-caller-identity")
	if err != nil {
		return err
	}
	if !found || !regexp.MustCompile(`^[0-9]{12}$`).MatchString(id.Account) || !strings.HasPrefix(id.Arn, "arn:aws:") {
		return errors.New("invalid AWS identity; this setup supports the standard AWS partition")
	}
	if old := values["AWS_ACCOUNT_ID"]; old != "" && old != id.Account {
		return fmt.Errorf("AWS account %s differs from existing environment account %s; use the correct AWS profile", id.Account, old)
	}
	if bootstrap != nil {
		if mode == "keys" {
			bootstrap.secrets.AWSAccess, bootstrap.secrets.AWSSecret, bootstrap.secrets.AWSToken = c.Access, c.Secret, c.Token
		} else {
			bootstrap.secrets.AWSAccess, bootstrap.secrets.AWSSecret, bootstrap.secrets.AWSToken = "", "", ""
		}
		if err := bootstrap.remember("authMode", mode); err != nil {
			return err
		}
		if err := bootstrap.remember("profile", profile); err != nil {
			return err
		}
		bootstrap.memory.Account = id.Account
		if err := bootstrap.saveMemory(); err != nil {
			return err
		}
	}
	fmt.Printf("AWS account: %s\nIdentity: %s\n", id.Account, id.Arn)
	if bootstrap != nil {
		if err = bootstrap.prepare(a, c, o, id.Account); err != nil {
			return err
		}
	}
	if err = checkBucket(&processRunner{env: a.Env}, o, id.Account); err != nil {
		return err
	}
	subject, err := defaultSubject(metadata, environment)
	if err != nil {
		return err
	}
	var customization struct {
		UseDefault bool `json:"use_default"`
	}
	found, err = g.Request("GET", "/repos/"+repo+"/actions/oidc/customization/sub", nil, &customization)
	if err != nil {
		return err
	}
	if !found || !customization.UseDefault {
		return errors.New("repository uses custom or unavailable OIDC subject settings; default repo/environment claims are required (settings left unchanged)")
	}
	// Older repositories may have opted into immutable subjects or been renamed.
	if metadata.CreatedAt.Before(time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)) {
		immutable := true
		if saved, ok := bootstrap.remembered("immutable"); ok {
			immutable = saved == "true"
		} else {
			if err = huh.NewConfirm().Title("Does this repository use immutable OIDC subjects (owner/repository IDs)?").Value(&immutable).Run(); err != nil {
				return err
			}
		}
		if bootstrap != nil {
			if err := bootstrap.remember("immutable", fmt.Sprint(immutable)); err != nil {
				return err
			}
		}
		if immutable {
			subject = fmt.Sprintf("repo:%s@%d/%s@%d:environment:%s", metadata.Owner.Login, metadata.Owner.ID, metadata.Name, metadata.ID, environment)
		}
	}
	name := "attestra-github-deploy-" + environment
	if old := values["AWS_ROLE_ARN"]; old != "" {
		parts := strings.Split(old, "/")
		name = parts[len(parts)-1]
	}
	if v, ok := bootstrap.remembered("roleName"); ok {
		name = v
	}
	if bootstrap == nil || (values["AWS_ROLE_ARN"] == "" && bootstrap.memory.Settings["roleName"] == "") {
		if err = ui.Input("Deployment role name (create or reuse setup-managed role)", &name, false, true).Validate(func(s string) error {
			if !regexp.MustCompile(`^[A-Za-z0-9_+=,.@-]{1,64}$`).MatchString(s) {
				return errors.New("invalid IAM role name")
			}
			return nil
		}).Run(); err != nil {
			return err
		}
	}
	if bootstrap != nil {
		if err := bootstrap.remember("roleName", name); err != nil {
			return err
		}
	}
	policies := deploymentPolicies(id.Account, o)
	if bootstrap != nil && bootstrap.indexAPIArn != "" {
		policies["attestra-index"] = indexPublicationPolicy(bootstrap.indexAPIArn, o.Stack)
	}
	iamDocument := policies["attestra-iam"].(map[string]any)
	iamDocument["Statement"] = append(iamDocument["Statement"].([]map[string]any), map[string]any{
		"Effect": "Deny", "Action": "iam:*", "Resource": []string{"arn:aws:iam::" + id.Account + ":role/" + name, "arn:aws:iam::" + id.Account + ":role/*/" + name},
	})
	zone, _ := bootstrap.remembered("route53")
	if bootstrap == nil {
		_, selectedZone, e := askDNSProvider("", "")
		if e != nil {
			return e
		}
		zone = selectedZone
		fmt.Println("IAM-only maintenance: DNS records are configured by the full setup wizard.")
	}
	if zone != "" {
		policies["attestra-dns"] = map[string]any{"Version": "2012-10-17", "Statement": []any{
			map[string]any{"Effect": "Allow", "Action": []string{"route53:GetHostedZone", "route53:ListResourceRecordSets", "route53:ChangeResourceRecordSets"}, "Resource": "arn:aws:route53:::hostedzone/" + zone},
			map[string]any{"Effect": "Allow", "Action": "route53:GetChange", "Resource": "arn:aws:route53:::change/*"},
		}}
	}
	bucketURL, _ := url.Parse(o.Backend)
	var encryption struct {
		ServerSideEncryptionConfiguration struct {
			Rules []struct {
				ApplyServerSideEncryptionByDefault struct{ SSEAlgorithm, KMSMasterKeyID string }
			}
		}
	}
	found, err = a.Call(&encryption, "s3api", "get-bucket-encryption", "--bucket", bucketURL.Host, "--expected-bucket-owner", id.Account)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("state bucket encryption configuration unavailable")
	}
	for _, rule := range encryption.ServerSideEncryptionConfiguration.Rules {
		algorithm := rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm
		if algorithm == "aws:kms" || algorithm == "aws:kms:dsse" {
			key := rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID
			if key == "" {
				key = "alias/aws/s3"
			}
			var result struct{ KeyMetadata struct{ Arn string } }
			ok, e := a.Call(&result, "kms", "describe-key", "--key-id", key)
			if e != nil {
				return e
			}
			if !ok || result.KeyMetadata.Arn == "" {
				return errors.New("cannot resolve the state encryption key")
			}
			policies["attestra-state-kms"] = map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": []string{"kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey", "kms:DescribeKey"}, "Resource": result.KeyMetadata.Arn, "Condition": map[string]any{"StringEquals": map[string]string{"kms:ViaService": "s3." + o.Region + ".amazonaws.com"}}}}}
		}
	}
	plan, err := inspectAWSSetup(a, id.Account, name, subject, environment, policies)
	if err != nil {
		return err
	}
	fmt.Printf("\nAWS role: %s\nProvider exists: %t; STS audience exists: %t; role exists: %t\nTrust policy:\n%s\n", name, plan.ProviderExists, plan.AudienceExists, plan.RoleExists, policyJSON(plan.Trust))
	fmt.Println("Deployment permissions include regional API Gateway and Cognito management; IAM permissions manage only application Lambda role names. Review full policies below.")
	for _, n := range sortedPolicyNames(policies) {
		fmt.Printf("\n%s:\n%s\n", n, policyJSON(policies[n]))
	}
	if bootstrap == nil {
		if err = ui.Confirm("Apply these AWS IAM changes in account " + id.Account + "?"); err != nil {
			return err
		}
	}
	arn, err := applyAWSSetup(a, plan)
	if err != nil {
		return fmt.Errorf("AWS setup stopped; successful changes are retained and can be resumed by rerunning: %w", err)
	}
	values["AWS_ACCOUNT_ID"], values["AWS_ROLE_ARN"] = id.Account, arn
	fmt.Println("✓ Verified AWS_ROLE_ARN:", arn)
	return nil
}
