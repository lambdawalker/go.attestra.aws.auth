package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type setupFake struct {
	subject                  string
	environment              string
	provider, audience, role bool
	trust                    map[string]any
	policies                 map[string]any
	writes                   []string
	fail                     string
	owned                    bool
}

func (f *setupFake) call(result any, args ...string) (bool, error) {
	op := args[1]
	if op == f.fail {
		return false, errors.New("AccessDenied")
	}
	value := func(flag string) string {
		for i, s := range args {
			if s == flag {
				return args[i+1]
			}
		}
		return ""
	}
	decode := func(v any) { b, _ := json.Marshal(v); _ = json.Unmarshal(b, result) }
	switch op {
	case "get-open-id-connect-provider":
		if !f.provider {
			return false, nil
		}
		aud := []string{}
		if f.audience {
			aud = append(aud, "sts.amazonaws.com")
		}
		decode(map[string]any{"Url": "token.actions.githubusercontent.com", "ClientIDList": aud})
	case "get-role":
		if !f.role {
			return false, nil
		}
		tags := []map[string]string{}
		if f.owned {
			tags = append(tags, map[string]string{"Key": "attestra-setup", "Value": "github-" + f.environment}, map[string]string{"Key": "attestra-subject", "Value": f.subject})
		}
		decode(map[string]any{"Role": map[string]any{"Arn": "arn:aws:iam::123456789012:role/attestra-github-deploy", "AssumeRolePolicyDocument": f.trust, "Tags": tags}})
	case "get-role-policy":
		p, ok := f.policies[value("--policy-name")]
		if !ok {
			return false, nil
		}
		decode(map[string]any{"PolicyDocument": p})
	default:
		f.writes = append(f.writes, op)
		switch op {
		case "create-open-id-connect-provider":
			f.provider = true
			f.audience = true
		case "add-client-id-to-open-id-connect-provider":
			f.audience = true
		case "create-role":
			for _, arg := range args {
				if strings.HasPrefix(arg, "Key=attestra-setup,Value=github-") {
					f.environment = strings.TrimPrefix(arg, "Key=attestra-setup,Value=github-")
				}
				if strings.HasPrefix(arg, "Key=attestra-subject,Value=") {
					f.subject = strings.TrimPrefix(arg, "Key=attestra-subject,Value=")
				}
			}
			f.role = true
			f.owned = true
			_ = json.Unmarshal([]byte(value("--assume-role-policy-document")), &f.trust)
		case "update-assume-role-policy":
			_ = json.Unmarshal([]byte(value("--policy-document")), &f.trust)
		case "put-role-policy":
			var p any
			_ = json.Unmarshal([]byte(value("--policy-document")), &p)
			f.policies[value("--policy-name")] = p
		}
	}
	return true, nil
}
func TestAWSSetupCreateAndRerun(t *testing.T) {
	f := &setupFake{policies: map[string]any{}}
	policies := deploymentPolicies("123456789012", options{Region: "us-east-2", Backend: "s3://bucket-state/team", Stack: "dev"})
	plan, e := inspectAWSSetup(f, "123456789012", "attestra-github-deploy", "repo:o/r:environment:dev", "dev", policies)
	if e != nil {
		t.Fatal(e)
	}
	arn, e := applyAWSSetup(f, plan)
	if e != nil {
		t.Fatal(e)
	}
	if arn != "arn:aws:iam::123456789012:role/attestra-github-deploy" {
		t.Fatal(arn)
	}
	f.writes = nil
	plan, e = inspectAWSSetup(f, "123456789012", "attestra-github-deploy", plan.Subject, "dev", policies)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = applyAWSSetup(f, plan); e != nil {
		t.Fatal(e)
	}
	if len(f.writes) != 0 {
		t.Fatal("rerun wrote unchanged configuration", f.writes)
	}
}
func TestAWSSetupDoesNotTreatAccessDeniedAsMissing(t *testing.T) {
	f := &setupFake{fail: "get-open-id-connect-provider"}
	if _, e := inspectAWSSetup(f, "123456789012", "deploy", "subject", "dev", nil); e == nil {
		t.Fatal("expected failure")
	}
	if len(f.writes) > 0 {
		t.Fatal(f.writes)
	}
}
func TestAWSSetupPreservesUnmanagedRole(t *testing.T) {
	f := &setupFake{provider: true, audience: true, role: true}
	if _, e := inspectAWSSetup(f, "123456789012", "deploy", "subject", "dev", nil); e == nil {
		t.Fatal("expected unmanaged role rejection")
	}
}
func TestAWSSetupPartialFailureCanResume(t *testing.T) {
	f := &setupFake{policies: map[string]any{}, fail: "put-role-policy"}
	policies := deploymentPolicies("123456789012", options{Region: "us-east-2", Backend: "s3://bucket-state"})
	p, e := inspectAWSSetup(f, "123456789012", "attestra-github-deploy", "subject", "dev", policies)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = applyAWSSetup(f, p); e == nil {
		t.Fatal("expected failure")
	}
	if !f.role || !f.provider {
		t.Fatal("partial changes lost")
	}
	f.fail = ""
	f.writes = nil
	p, e = inspectAWSSetup(f, p.Account, p.RoleName, p.Subject, "dev", policies)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = applyAWSSetup(f, p); e != nil {
		t.Fatal(e)
	}
	for _, op := range f.writes {
		if op == "create-role" || op == "create-open-id-connect-provider" {
			t.Fatal("recreated existing resource")
		}
	}
}
func TestAWSSetupAddsAudienceAndRepairsOwnedTrust(t *testing.T) {
	f := &setupFake{provider: true, role: true, owned: true, environment: "dev", subject: "subject", policies: map[string]any{}, trust: map[string]any{}}
	p, e := inspectAWSSetup(f, "123456789012", "attestra-github-deploy", "subject", "dev", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = applyAWSSetup(f, p); e != nil {
		t.Fatal(e)
	}
	if !f.audience || len(f.writes) != 2 {
		t.Fatal(f.writes)
	}
}
func TestDeploymentPoliciesScopeStateAndIAM(t *testing.T) {
	p := deploymentPolicies("123456789012", options{Region: "us-east-2", Backend: "s3://bucket-state/team"})
	s := policyJSON(p)
	if !strings.Contains(s, "arn:aws:s3:::bucket-state/team/.pulumi/*") {
		t.Fatal("wrong state prefix")
	}
	if strings.Contains(s, "AdministratorAccess") || strings.Contains(s, `"iam:*"`) || strings.Contains(s, "role/attestra-github-deploy") {
		t.Fatal("deployment can manage its own role")
	}
	if !strings.Contains(s, "iam:PassedToService") {
		t.Fatal("unrestricted PassRole")
	}
}
func TestDefaultSubjectUsesRepositoryMetadata(t *testing.T) {
	var m repositoryMetadata
	m.ID = 123
	m.Name = "repo"
	m.Owner.ID = 456
	m.Owner.Login = "owner"
	m.CreatedAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	got, e := defaultSubject(m, "dev")
	if e != nil || got != "repo:owner@456/repo@123:environment:dev" {
		t.Fatal(got, e)
	}
	m.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	got, e = defaultSubject(m, "dev")
	if e != nil || got != "repo:owner/repo:environment:dev" {
		t.Fatal(got, e)
	}
}

func TestAWSRoleCannotBeReusedAcrossEnvironments(t *testing.T) {
	f := &setupFake{provider: true, audience: true, role: true, owned: true, environment: "dev", subject: "repo:o/r:environment:dev"}
	if _, err := inspectAWSSetup(f, "123456789012", "attestra-github-deploy", "repo:o/r:environment:qa", "qa", nil); err == nil {
		t.Fatal("allowed QA to take ownership of dev role")
	}
	if len(f.writes) != 0 {
		t.Fatal(f.writes)
	}
}
func TestAWSSetupCustomEnvironmentRoundTrip(t *testing.T) {
	f := &setupFake{policies: map[string]any{}}
	p, err := inspectAWSSetup(f, "123456789012", "attestra-github-deploy-demo", "repo:o/r:environment:demo", "demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyAWSSetup(f, p); err != nil {
		t.Fatal(err)
	}
	if f.environment != "demo" || f.subject != "repo:o/r:environment:demo" {
		t.Fatal(f)
	}
}
