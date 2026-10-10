package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVaultManagementUsesSetupRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bootstrap.qa.local.json"), []byte(`{"Repository":"different/fork"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := credentialRepository(root, "qa", "origin/repo"); got != "different/fork" {
		t.Fatal(got)
	}
}
