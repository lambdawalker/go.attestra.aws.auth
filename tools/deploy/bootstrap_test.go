package main

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBootstrapStackNames(t *testing.T) {
	for _, s := range []string{`[{"name":"dev"}]`, `[{"name":"org/attestra-auth-email/dev"}]`} {
		found, err := stackListed([]byte(s), "dev")
		if err != nil || !found {
			t.Fatal(found, err)
		}
	}
	if _, err := stackListed([]byte(`bad`), "dev"); err == nil {
		t.Fatal("malformed list accepted")
	}
	if found, _ := stackListed([]byte(`[{"name":"qa"}]`), "dev"); found {
		t.Fatal("wrong stack matched")
	}
}
func TestArchiveHasLinuxExecutableBootstrap(t *testing.T) {
	dir := t.TempDir()
	binary, archive := filepath.Join(dir, "bootstrap"), filepath.Join(dir, "capture.zip")
	if err := os.WriteFile(binary, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := packageLambda(binary, archive); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "bootstrap" || z.File[0].Mode().Perm() != 0755 {
		t.Fatal("archive lacks executable bootstrap")
	}
}

type bootstrapFake struct {
	calls                     []string
	list, config, state, fail string
}

func (f *bootstrapFake) Exec(_ string, _ bool, name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.fail != "" && strings.Contains(cmd, f.fail) {
		return nil, errors.New("test failure")
	}
	switch {
	case strings.Contains(cmd, "stack ls"):
		return []byte(f.list), nil
	case strings.Contains(cmd, "stack export"):
		return []byte(f.state), nil
	case strings.Contains(cmd, "config --json"):
		return []byte(f.config), nil
	}
	return nil, nil
}
func TestBootstrapNeverInitializesOnReadFailure(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{fail: "stack ls"}
	err := bootstrapStack(f, o, nil, func(string) error { return nil }, func(string) error { t.Fatal("secret write after failure"); return nil })
	if err == nil || len(f.calls) != 1 {
		t.Fatal(err, f.calls)
	}
}
func TestBootstrapRetainsExistingProofAndConfiguration(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[{"name":"dev"}]`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, config: `{"aws:region":{"value":"us-east-2"},"attestra-auth-email:proofKey":{"value":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","secret":true},"attestra-auth-email:appOrigin":{"value":"https://existing.example"}}`}
	err := bootstrapStack(f, o, map[string]string{"attestra-auth-email:appOrigin": "https://new.example"}, func(string) error { return nil }, func(string) error { t.Fatal("existing proof rotated"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range f.calls {
		if strings.Contains(cmd, "config set") || strings.Contains(cmd, "stack init") {
			t.Fatal("existing config mutated", cmd)
		}
	}
}
func TestBootstrapRejectsWrongPassphraseBeforeConfigWrites(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[{"name":"dev"}]`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, fail: "config --json"}
	if bootstrapStack(f, o, nil, func(string) error { return nil }, func(string) error { return nil }) == nil {
		t.Fatal("accepted unreadable secrets")
	}
	for _, cmd := range f.calls {
		if strings.Contains(cmd, "config set") {
			t.Fatal(cmd)
		}
	}
}

func TestBootstrapRejectsUnreadableBackendSecretsBeforeWrites(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[{"name":"dev"}]`, fail: "stack export"}
	if bootstrapStack(f, o, nil, func(string) error { return nil }, func(string) error { t.Fatal("secret changed after backend read failure"); return nil }) == nil {
		t.Fatal("accepted unreadable backend secrets")
	}
	if got := f.calls[len(f.calls)-1]; !strings.Contains(got, "stack export --show-secrets") {
		t.Fatal("backend secrets were not checked", got)
	}
}

type bootstrapBucketFake struct {
	headErr error
	region  string
	calls   []string
	fail    string
}

func (f *bootstrapBucketFake) call(result any, args ...string) (bool, error) {
	f.calls = append(f.calls, args[1])
	if args[1] == f.fail {
		return false, errors.New("test failure")
	}
	if args[1] == "head-bucket" {
		return f.headErr == nil, f.headErr
	}
	if args[1] == "get-bucket-location" {
		b := []byte(`{"LocationConstraint":"` + f.region + `"}`)
		return true, json.Unmarshal(b, result)
	}
	return true, nil
}
func TestBucketAccessDeniedNeverCreates(t *testing.T) {
	f := &bootstrapBucketFake{headErr: &awsSetupError{Code: "403"}}
	if ensureStateBucket(f, testOptions(), "123456789012", func(string) error { t.Fatal("prompted after access denial"); return nil }) == nil {
		t.Fatal("accepted denial")
	}
	if len(f.calls) != 1 {
		t.Fatal(f.calls)
	}
}
func TestBucketCreatesOnlyOnNotFoundAndStopsOnWriteFailure(t *testing.T) {
	for _, failure := range []string{"", "create-bucket", "put-public-access-block", "put-bucket-versioning"} {
		f := &bootstrapBucketFake{headErr: &awsSetupError{Code: "404"}, region: "us-east-2", fail: failure}
		err := ensureStateBucket(f, testOptions(), "123456789012", func(string) error { return nil })
		if failure == "" && err != nil {
			t.Fatal(err)
		}
		if failure != "" && (err == nil || f.calls[len(f.calls)-1] != failure) {
			t.Fatal(err, f.calls)
		}
	}
}
func TestBucketWrongRegionDoesNotModify(t *testing.T) {
	f := &bootstrapBucketFake{region: "us-west-2"}
	if ensureStateBucket(f, testOptions(), "123456789012", func(string) error { return nil }) == nil {
		t.Fatal("accepted wrong region")
	}
	if len(f.calls) != 2 {
		t.Fatal(f.calls)
	}
}
func TestBootstrapFreshStackBacksUpConfigAndGeneratesKey(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	dir := filepath.Join(o.Root, "infra")
	os.Mkdir(dir, 0700)
	path := filepath.Join(dir, "Pulumi.dev.yaml")
	os.WriteFile(path, []byte("old encrypted config"), 0600)
	f := &bootstrapFake{list: `[]`, config: `{}`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`}
	count := 0
	err := bootstrapStack(f, o, map[string]string{"aws:region": "us-east-2"}, func(string) error { return nil }, func(key string) error {
		raw, e := base64.StdEncoding.DecodeString(key)
		if e != nil || len(raw) != 32 {
			t.Fatal("invalid generated proof")
		}
		count++
		return nil
	})
	if err != nil || count != 1 {
		t.Fatal(err, count)
	}
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	b, _ := os.ReadFile(backups[0])
	if string(b) != "old encrypted config" {
		t.Fatal("backup changed")
	}
}
func TestBootstrapDeclineLeavesConfigUntouched(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[]`}
	if bootstrapStack(f, o, nil, func(string) error { return errors.New("cancelled") }, func(string) error { t.Fatal("secret write"); return nil }) == nil {
		t.Fatal("ignored cancellation")
	}
	if len(f.calls) != 1 {
		t.Fatal(f.calls)
	}
}
func TestBootstrapStopsAtEachStackWrite(t *testing.T) {
	for _, failure := range []string{"stack init", "config set"} {
		o := testOptions()
		o.Root = t.TempDir()
		f := &bootstrapFake{list: `[]`, config: `{}`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, fail: failure}
		if bootstrapStack(f, o, map[string]string{"aws:region": "us-east-2"}, func(string) error { return nil }, func(string) error { t.Fatal("continued after failure"); return nil }) == nil {
			t.Fatal("ignored failure")
		}
		if !strings.Contains(f.calls[len(f.calls)-1], failure) {
			t.Fatal(f.calls)
		}
	}
}
func TestBootstrapAppValidation(t *testing.T) {
	if err := validateBootstrapApp("https://app.example.com", "info.example.com", "verify@info.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://app.example.com", "https://app.example.com/path", "https://user:pass@app.example.com"} {
		if validateBootstrapApp(origin, "info.example.com", "verify@info.example.com") == nil {
			t.Fatal(origin)
		}
	}
	if validateBootstrapApp("https://app.example.com", "info.example.com", "verify@other.example.com") == nil {
		t.Fatal("mismatched sender accepted")
	}
}
func TestLegacyWindowsArguments(t *testing.T) {
	got := normalizeArguments([]string{"-Backend", "s3://bucket/CaseSensitive", "-MigrateFrom", "o/attestra-auth-email/dev", "-Sso", "-Profile=MyProfile"})
	want := []string{"-backend", "s3://bucket/CaseSensitive", "-migrate-from", "o/attestra-auth-email/dev", "-sso", "-profile=MyProfile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestExistingStackWithoutLocalProofNeverRotates(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[{"name":"dev"}]`, config: `{}`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"},"resources":[{}]}}`}
	if bootstrapStack(f, o, map[string]string{"aws:region": "us-east-2"}, func(string) error { return nil }, func(string) error { t.Fatal("rotated existing key"); return nil }) == nil {
		t.Fatal("accepted missing config")
	}
	for _, cmd := range f.calls {
		if strings.Contains(cmd, "config set") {
			t.Fatal(cmd)
		}
	}
}
func TestCheckpointRetainsBucketWithoutStoringSecrets(t *testing.T) {
	w := &bootstrapWizard{root: t.TempDir()}
	values := map[string]string{"AWS_REGION": "us-east-2", "PULUMI_BACKEND_URL": "s3://existing-state", "PULUMI_STACK": "dev", "PULUMI_CONFIG_PASSPHRASE": "never-save-this"}
	if err := w.saveSelections("o/r", values); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(w.root, "bootstrap.local.json"))
	if strings.Contains(string(b), "never-save-this") {
		t.Fatal("secret leaked")
	}
	defaults := map[string]string{}
	if err := w.loadSelections("o/r", defaults, nil); err != nil {
		t.Fatal(err)
	}
	if defaults["PULUMI_BACKEND_URL"] != "s3://existing-state" {
		t.Fatal(defaults)
	}
	defaults["PULUMI_BACKEND_URL"] = "s3://github-state"
	if err := w.loadSelections("o/r", defaults, map[string]string{"PULUMI_BACKEND_URL": "s3://github-state"}); err != nil {
		t.Fatal(err)
	}
	if defaults["PULUMI_BACKEND_URL"] != "s3://github-state" {
		t.Fatal("checkpoint overrode GitHub settings")
	}
}
