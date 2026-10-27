package workflow

import (
	"bytes"
	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"
	"os"
	"path/filepath"
	"testing"
)

func TestDeployReadsBootstrapSettings(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".attestra"), 0700)
	path := filepath.Join(root, ".attestra", "bootstrap.dev.local.json")
	original := `{"Repository":"owner/repo","Stack":"dev","Backend":"s3://saved","Region":"us-east-2","Settings":{"authMode":"sso","profile":"attestra"}}`
	os.WriteFile(path, []byte(original), 0600)
	saved, err := readDeploySettings(root, "dev", "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	o := options{Root: root, Stack: "dev", Region: "default", Profile: "default"}
	applyDeploySettings(&o, saved, map[string]bool{})
	if o.Backend != "s3://saved" || o.Region != "us-east-2" || !o.Sso || o.Profile != "attestra" {
		t.Fatalf("settings not reused: %+v", o)
	}
	if err := saveDeploySettings(o, "owner/repo"); err != nil {
		t.Fatal(err)
	}
	// Windows replacement and reruns must work too.
	if err := saveDeploySettings(o, "owner/repo"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != original {
		t.Fatal("bootstrap progress overwritten")
	}
	again, err := readDeploySettings(root, "dev", "owner/repo")
	if err != nil || again != saved {
		t.Fatalf("round trip: %+v %v", again, err)
	}
	qa, err := readDeploySettings(root, "qa", "owner/repo")
	if err != nil || qa.Backend != "" {
		t.Fatal("dev settings leaked into qa")
	}
	if _, err := readDeploySettings(root, "dev", "different/repo"); err == nil {
		t.Fatal("accepted other repository")
	}
	if _, err := readDeploySettings(root, "../dev", "owner/repo"); err == nil {
		t.Fatal("accepted unsafe stack")
	}
}

func TestDeployExplicitFlagsWin(t *testing.T) {
	saved := deploySettings{Backend: "s3://saved", Region: "old", Profile: "old", AuthMode: "sso"}
	o := options{Backend: "s3://explicit", Region: "new", Profile: "new", Login: true}
	applyDeploySettings(&o, saved, map[string]bool{"backend": true, "region": true, "profile": true, "login": true})
	if o.Backend != "s3://explicit" || o.Region != "new" || o.Profile != "new" || !o.Login || o.Sso {
		t.Fatalf("flags overwritten: %+v", o)
	}
}

func TestDeployRejectsCorruptSettings(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".attestra"), 0700)
	os.WriteFile(filepath.Join(root, ".attestra", "deploy.dev.local.json"), []byte("broken"), 0600)
	if _, err := readDeploySettings(root, "dev", "owner/repo"); err == nil {
		t.Fatal("silently ignored corrupt settings")
	}
}

func TestTeardownCleansOnlySelectedDeploySettings(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "qa"} {
		if err := saveDeploySettings(options{Root: root, Stack: env, Backend: "s3://bucket"}, "owner/repo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupTeardownLocal(root, &teardownProgress{Environment: "qa", Repository: "owner/repo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".attestra", "deploy.qa.local.json")); !os.IsNotExist(err) {
		t.Fatal("qa settings retained")
	}
	if _, err := os.Stat(filepath.Join(root, ".attestra", "deploy.dev.local.json")); err != nil {
		t.Fatal("dev settings removed")
	}
}

func TestDeploySavesSecretsWithoutReplacingOtherVaultCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.vault.enc")
	phrase := "harbor lichen orbit meadow lantern 527"
	vault, err := credentialvault.Create(path, "test-scope", phrase)
	if err != nil {
		t.Fatal(err)
	}
	w := &bootstrapWizard{vault: vault, passphrase: "pulumi-private-value", secrets: credentialvault.Credentials{GitHub: "github-private-value", Cloudflare: "cf-private-value", IndexPulumi: "index-private-value", AWSAccess: "access-private-value", AWSSecret: "secret-private-value"}}
	if err := w.saveCredentialsPrompt(false); err != nil {
		t.Fatal(err)
	}
	vault.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{phrase, "pulumi-private-value", "github-private-value", "secret-private-value"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("plaintext secret written")
		}
	}
	reopened, values, err := credentialvault.Unlock(path, "test-scope", phrase)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if values.Pulumi != "pulumi-private-value" || values.GitHub != "github-private-value" || values.Cloudflare != "cf-private-value" || values.IndexPulumi != "index-private-value" {
		t.Fatal("other vault credentials lost")
	}
}
