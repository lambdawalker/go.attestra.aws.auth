package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
	"os"
	"path/filepath"
	"strings"
)

func validateDeployScope(scope string, flags []string) error {
	if scope == "app" {
		return nil
	}
	if scope != "shared" {
		return errors.New("-scope must be app or shared")
	}
	allowed := map[string]bool{"scope": true, "repo-root": true, "region": true, "profile": true, "sso": true, "login": true}
	for _, flag := range flags {
		if !allowed[flag] {
			return fmt.Errorf("-%s cannot be combined with -scope shared; shared deployment uses its own existing stack and backend", flag)
		}
	}
	return nil
}

func runSharedDeploy(o options) error {
	root := o.Root
	o.Stack = "shared"
	if o.Sso && o.Login {
		return errors.New("choose either -sso or -login")
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("shared deployment requires an interactive terminal")
	}
	if err := (&bootstrapWizard{root: root}).preflight(); err != nil {
		return err
	}
	fmt.Println("Attestra • Deploy shared index and admin")
	vaultEnv := ""
	if err := ui.Input("Environment whose credential vault to unlock (optional)", &vaultEnv, false, false).Run(); err != nil {
		return err
	}
	saved := &bootstrapWizard{root: root, environment: strings.TrimSpace(vaultEnv)}
	defer saved.cleanup()
	if saved.environment != "" {
		if err := ui.ValidateEnvironment(saved.environment); err != nil {
			return err
		}
		repo := teardownRepository(root, saved.environment)
		if repo == "" {
			repo = "lambdawalker/go.attestra.aws.auth"
		}
		if err := saved.loadCredentials(repo, false); err != nil {
			return err
		}
	}
	mode := "sso"
	if o.Login {
		mode = "login"
	}
	if !o.Sso && !o.Login {
		if err := huh.NewSelect[string]().Title("AWS administrator authentication").Options(huh.NewOption("SSO", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Access key credentials", "keys")).Value(&mode).Run(); err != nil {
			return err
		}
	}
	c := credentials{Access: saved.secrets.AWSAccess, Secret: saved.secrets.AWSSecret, Token: saved.secrets.AWSToken}
	var err error
	if mode == "keys" {
		if c.Access == "" || c.Secret == "" {
			if err = huh.NewForm(huh.NewGroup(ui.Input("AWS access key ID", &c.Access, true, true), ui.Input("AWS secret access key", &c.Secret, true, true), ui.Input("AWS session token", &c.Token, true, false))).Run(); err != nil {
				return err
			}
		}
	} else {
		if o.Profile == "default" {
			o.Profile = "attestra"
		}
		if err = ui.Input("AWS profile", &o.Profile, false, true).Run(); err != nil {
			return err
		}
		o.Sso, o.Login = mode == "sso", mode == "login"
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return err
		}
	}
	c.Passphrase = saved.secrets.IndexPulumi
	if c.Passphrase == "" {
		if err = ui.Input("Existing shared index Pulumi passphrase", &c.Passphrase, true, true).Run(); err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "attestra-shared-deploy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	empty := filepath.Join(tmp, "aws-config")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env, err := prepareCloudEnvironment(os.Environ(), c, o, empty)
	if err != nil {
		return err
	}
	a := &awsenv.Client{Env: env, RenewalFailure: credentialRenewalFailure}
	var identity struct{ Account, Arn string }
	if _, err = a.Call(&identity, "sts", "get-caller-identity"); err != nil {
		return err
	}
	if len(identity.Account) != 12 {
		return errors.New("invalid AWS account response")
	}
	o.Backend = "s3://attestra-index-state-" + identity.Account + "-" + o.Region
	if err = o.validate(); err != nil {
		return err
	}
	r := &processRunner{env: replaceEnvironment(env, map[string]string{"PULUMI_BACKEND_URL": o.backendURL()})}
	if err = checkBucket(r, o, identity.Account); err != nil {
		return err
	}
	return withSharedDeployLock(a, o, identity.Account, tmp, func() error { return deploySharedStack(r, o, buildIndex) })
}

func withSharedDeployLock(a awsenv.API, o options, account, tmp string, work func() error) (err error) {
	bucket := strings.TrimPrefix(o.Backend, "s3://")
	body := filepath.Join(tmp, "shared-deploy-lock.json")
	if err = os.WriteFile(body, []byte(`{"operation":"deploy-shared"}`), 0600); err != nil {
		return err
	}
	var lease struct{ ETag string }
	if _, err = a.Call(&lease, "s3api", "put-object", "--bucket", bucket, "--key", "bootstrap.lock", "--body", body, "--if-none-match", "*", "--expected-bucket-owner", account); err != nil {
		return fmt.Errorf("shared setup/deploy/teardown lock unavailable: %w", err)
	}
	if lease.ETag == "" {
		return errors.New("shared lock response missing ETag; inspect bootstrap.lock before retrying")
	}
	defer func() {
		if _, e := a.Call(nil, "s3api", "delete-object", "--bucket", bucket, "--key", "bootstrap.lock", "--if-match", lease.ETag, "--expected-bucket-owner", account); e != nil {
			err = errors.Join(err, fmt.Errorf("could not release shared bootstrap.lock: %w", e))
		}
	}()
	if err = checkIndexTeardownPending(a, bucket, account); err != nil {
		return err
	}
	return work()
}

func deploySharedStack(r commandRunner, o options, build func(string) error) error {
	dir := filepath.Join(o.Root, "infra-index")
	run := func(capture bool, args ...string) ([]byte, error) { return r.Exec(dir, capture, "pulumi", args...) }
	if _, err := run(true, "stack", "select", "shared", "--non-interactive"); err != nil {
		return fmt.Errorf("cannot select existing shared stack; run setup for first-time provisioning: %w", err)
	}
	data, err := run(true, "stack", "export", "--stack", "shared")
	if err != nil {
		return err
	}
	var state struct {
		Deployment struct {
			SecretsProviders struct{ Type string } `json:"secrets_providers"`
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment.SecretsProviders.Type != "passphrase" {
		return errors.New("existing shared stack must use passphrase encryption")
	}
	if _, err = run(true, "config", "refresh", "--stack", "shared", "--force", "--non-interactive"); err != nil {
		return err
	}
	data, err = run(true, "config", "--json", "--stack", "shared")
	if err != nil {
		return err
	}
	var cfg map[string]struct{ Value string }
	if json.Unmarshal(data, &cfg) != nil {
		return errors.New("invalid shared configuration")
	}
	for k, v := range cfg {
		if strings.HasPrefix(k, "aws:") && (k != "aws:region" || v.Value != o.Region) {
			return errors.New("shared AWS provider configuration requires review")
		}
	}
	if cfg["aws:region"].Value != o.Region || cfg["attestra-index:domain"].Value == "" || cfg["attestra-index:adminDomain"].Value == "" {
		return errors.New("shared configuration incomplete; run setup first")
	}
	// Read existing outputs before building: this command updates an initialized deployment.
	data, err = run(true, "stack", "output", "--json", "--stack", "shared")
	if err != nil {
		return err
	}
	var out map[string]string
	if json.Unmarshal(data, &out) != nil || out["adminPoolId"] == "" || out["adminLoginDomain"] == "" || out["runtimeTableName"] == "" || out["apiUrl"] == "" {
		return errors.New("shared deployment incomplete; resume setup first")
	}
	if err = build(o.Root); err != nil {
		return err
	}
	if _, err = run(false, "preview", "--stack", "shared"); err != nil {
		return err
	}
	if _, err = run(false, "up", "--stack", "shared"); err != nil {
		return err
	}
	data, err = run(true, "stack", "output", "--json", "--stack", "shared")
	if err != nil {
		return err
	}
	out = nil
	if json.Unmarshal(data, &out) != nil || out["adminPoolId"] == "" || out["adminLoginDomain"] == "" {
		return errors.New("shared deployment completed but admin outputs are incomplete")
	}
	if _, err = r.Exec(o.Root, false, "go", "run", "./cmd/admin-setup", "-auth-only", "-pool", out["adminPoolId"], "-login", out["adminLoginDomain"]); err != nil {
		return fmt.Errorf("shared infrastructure updated; admin authentication reconciliation incomplete, rerun shared deployment: %w", err)
	}
	fmt.Println("Shared index and admin deployment finished. Environment backends were not deployed.")
	return nil
}
