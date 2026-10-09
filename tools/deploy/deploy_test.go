package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls         []string
	fail          int
	state, config string
}

func (f *fakeRunner) Exec(_ string, _ bool, name string, args ...string) ([]byte, error) {
	command := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, command)
	if f.fail > 0 && len(f.calls) == f.fail {
		return nil, errors.New("stub failure")
	}
	switch {
	case command == "pulumi version":
		return []byte("v3.254.0"), nil
	case strings.Contains(command, "stack export"):
		return []byte(f.state), nil
	case strings.Contains(command, "config --json"):
		return []byte(f.config), nil
	default:
		return nil, nil
	}
}
func goodRunner() *fakeRunner {
	return &fakeRunner{state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, config: `{"aws:region":{"value":"us-east-2"}}`}
}
func testOptions() options {
	return options{Root: "/repo", Stack: "dev", Region: "us-east-2", Backend: "s3://attestra-state"}
}
func TestDeploymentStopsAtEveryFailure(t *testing.T) {
	good := goodRunner()
	if err := execute(good, testOptions()); err != nil {
		t.Fatal(err)
	}
	if len(good.calls) != 6 || good.calls[4] != "pulumi preview --stack dev" || good.calls[5] != "pulumi up --stack dev" {
		t.Fatal(good.calls)
	}
	for i := 1; i <= len(good.calls); i++ {
		r := goodRunner()
		r.fail = i
		if err := execute(r, testOptions()); err == nil {
			t.Fatalf("step %d did not fail", i)
		}
		if len(r.calls) != i {
			t.Fatalf("step %d continued: %v", i, r.calls)
		}
	}
}
func TestMigrationPreservesSourceAndNeverDeploys(t *testing.T) {
	o := testOptions()
	o.MigrateFrom = "isdavid/attestra-auth-email/dev"
	r := goodRunner()
	if err := execute(r, o); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pulumi version",
		"pulumi stack migrate https://api.pulumi.com isdavid/attestra-auth-email/dev --target dev --secrets-provider passphrase",
		"pulumi config rm aws:profile --stack dev",
		"pulumi stack export --stack dev",
		"pulumi config --json --stack dev",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatal(r.calls)
	}
	for i := 1; i <= len(want); i++ {
		r := goodRunner()
		r.fail = i
		if execute(r, o) == nil || len(r.calls) != i {
			t.Fatalf("migration continued after failure %d: %v", i, r.calls)
		}
	}
}
func TestStackGuards(t *testing.T) {
	for _, config := range []string{
		`{"aws:region":{"value":"us-west-2"}}`,
		`{"aws:region":{"value":"us-east-2"},"aws:profile":{"value":"attestra"}}`,
		`{"aws:region":{"value":"us-east-2"},"aws:accessKey":{"value":"old"}}`,
	} {
		r := goodRunner()
		r.config = config
		if execute(r, testOptions()) == nil || len(r.calls) != 3 {
			t.Fatal(r.calls)
		}
	}
	r := goodRunner()
	r.state = `{"deployment":{"secrets_providers":{"type":"service"}}}`
	if execute(r, testOptions()) == nil || len(r.calls) != 2 {
		t.Fatal("accepted Cloud encryption")
	}
}
func TestCredentialIsolation(t *testing.T) {
	original := []string{"PATH=/bin", "AWS_PROFILE=old", "aws_session_token=stale", "AWS_ENDPOINT_URL=http://untrusted", "PULUMI_ACCESS_TOKEN=old", "PULUMI_CONFIG_PASSPHRASE_FILE=old", "GOOS=linux", "GOARCH=arm64"}
	o := testOptions()
	c := credentials{Access: "new-id", Secret: "new-secret", Passphrase: "new-pass", CloudToken: "migration-only"}
	env := cloudEnvironment(original, c, o, "/empty")
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"old", "stale", "untrusted", "migration-only", "GOARCH", "GOOS"} {
		if strings.Contains(joined, bad) {
			t.Fatal("leaked inherited environment:", bad)
		}
	}
	for _, good := range []string{"AWS_SESSION_TOKEN=\n", "AWS_ACCESS_KEY_ID=new-id", "PULUMI_BACKEND_URL=s3://attestra-state?awssdk=v2&region=us-east-2"} {
		if !strings.Contains(joined, good) {
			t.Fatal("missing setting:", good)
		}
	}
	if !reflect.DeepEqual(withoutCredentials(original), []string{"PATH=/bin"}) {
		t.Fatal("build/git receive secrets")
	}
	o.MigrateFrom = "isdavid/attestra-auth-email/dev"
	if !strings.Contains(strings.Join(cloudEnvironment(original, c, o, "/empty"), "\n"), "PULUMI_ACCESS_TOKEN=migration-only") {
		t.Fatal("missing migration token")
	}
}
func TestOptionsAndBuildPlatforms(t *testing.T) {
	for _, backend := range []string{"https://api.pulumi.com", "s3://user:secret@bucket", "s3://bucket?profile=bad", "s3://bucket/../bad", "s3://bucket#fragment"} {
		o := testOptions()
		o.Backend = backend
		if o.validate() == nil {
			t.Fatal("accepted", backend)
		}
	}
	o := testOptions()
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	o.MigrateFrom = "isdavid/attestra-auth-email/production"
	if o.validate() == nil {
		t.Fatal("accepted different source stack")
	}
	command, args := buildCommand("windows")
	if command != "powershell.exe" || args[len(args)-1] != "build.ps1" {
		t.Fatal(command, args)
	}
	command, args = buildCommand("linux")
	if command != "bash" || !reflect.DeepEqual(args, []string{"build.sh"}) {
		t.Fatal(command, args)
	}
}

type bucketRunner struct {
	fail             int
	version, privacy string
	calls            int
}

func (r *bucketRunner) Exec(_ string, _ bool, _ string, args ...string) ([]byte, error) {
	r.calls++
	if r.calls == r.fail {
		return nil, errors.New("access denied")
	}
	if args[1] == "get-bucket-versioning" {
		return []byte(r.version), nil
	}
	if args[1] == "get-public-access-block" {
		return []byte(r.privacy), nil
	}
	return nil, nil
}
func TestBucketGuards(t *testing.T) {
	good := func() *bucketRunner {
		return &bucketRunner{version: `{"Status":"Enabled"}`, privacy: `{"PublicAccessBlockConfiguration":{"BlockPublicAcls":true,"IgnorePublicAcls":true,"BlockPublicPolicy":true,"RestrictPublicBuckets":true}}`}
	}
	if err := checkBucket(good(), testOptions(), "123456789012"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		r := good()
		r.fail = i
		if checkBucket(r, testOptions(), "123456789012") == nil || r.calls != i {
			t.Fatalf("continued after bucket failure %d", i)
		}
	}
	r := good()
	r.version = `{"Status":"Suspended"}`
	if checkBucket(r, testOptions(), "123456789012") == nil {
		t.Fatal("accepted suspended versioning")
	}
	r = good()
	r.privacy = `{"PublicAccessBlockConfiguration":{"BlockPublicAcls":true}}`
	if checkBucket(r, testOptions(), "123456789012") == nil {
		t.Fatal("accepted incomplete public access block")
	}
}
