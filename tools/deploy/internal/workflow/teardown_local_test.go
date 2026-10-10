package workflow

import (
	"os"
	"path/filepath"
	"testing"

	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"
)

func TestTeardownLocalCleanupIsEnvironmentScoped(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "android-config"), 0700)
	for _, env := range []string{"qa", "dev"} {
		os.WriteFile(filepath.Join(root, "bootstrap."+env+".local.json"), []byte(`{"Repository":"owner/repo","Environment":"`+env+`"}`), 0600)
		os.WriteFile(filepath.Join(root, "android-config", env+".properties"), []byte("public"), 0600)
	}
	p := teardownProgress{Repository: "owner/repo", Environment: "qa"}
	if err := cleanupTeardownLocal(root, &p); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bootstrap.qa.local.json", "android-config/qa.properties"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("stale local file retained")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "bootstrap.dev.local.json")); err != nil {
		t.Fatal("other environment removed")
	}
	if err := cleanupTeardownLocal(root, &p); err != nil {
		t.Fatal("cleanup cannot resume", err)
	}
}
func TestTeardownLocalCleanupRejectsOtherRepository(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "bootstrap.qa.local.json"), []byte(`{"Repository":"other/repo","Environment":"qa"}`), 0600)
	if err := cleanupTeardownLocal(root, &teardownProgress{Repository: "owner/repo", Environment: "qa"}); err == nil {
		t.Fatal("foreign local setup deleted")
	}
}

func TestTeardownVaultDeletionRequiresSavedChoice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	path, _, err := credentialvault.Location("owner/repo", "qa")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("encrypted"), 0600)
	p := teardownProgress{Repository: "owner/repo", Environment: "qa"}
	if err = cleanupTeardownLocal(root, &p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("vault removed by default")
	}
	p.DeleteVault = true
	if err = cleanupTeardownLocal(root, &p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("approved vault deletion not applied")
	}
}
func TestTeardownDiscoveryAndRepositorySurviveSetupRemoval(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "teardown.preview-7.local.json"), []byte(`{"Repository":"owner/fork","Environment":"preview-7"}`), 0600)
	names := teardownEnvironments(root)
	if len(names) != 1 || names[0] != "preview-7" {
		t.Fatal(names)
	}
	if got := teardownRepository(root, "preview-7"); got != "owner/fork" {
		t.Fatal(got)
	}
}

func TestTeardownRemovesLegacyDevCheckpoint(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "bootstrap.local.json")
	os.WriteFile(legacy, []byte(`{"Repository":"owner/repo","Stack":"dev","Stage":"complete"}`), 0600)
	if err := cleanupTeardownLocal(root, &teardownProgress{Repository: "owner/repo", Environment: "dev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy completion can resurrect deleted environment")
	}
}
