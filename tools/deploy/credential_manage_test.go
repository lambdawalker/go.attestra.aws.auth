package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVaultPassphraseRotationPreservesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	old := "violet lantern glacier copper meadow"
	next := "silver orchard thunder velvet comet"
	v, e := createVault(path, "scope", old)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(v.key)
	values := savedCredentials{GitHub: "github-value", Cloudflare: "cf-value", Pulumi: "pulumi-value"}
	if e = v.save(values); e != nil {
		t.Fatal(e)
	}
	rotated, e := rotateVaultPassphrase(v, values, next)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(rotated.key)
	if _, _, e = unlockVault(path, "scope", old); e == nil {
		t.Fatal("old passphrase still unlocks")
	}
	unlocked, got, e := unlockVault(path, "scope", next)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(unlocked.key)
	if got != values {
		t.Fatal("rotation changed stored credentials")
	}
	if _, e = rotateVaultPassphrase(rotated, values, "weak"); e == nil {
		t.Fatal("weak passphrase accepted")
	}
}

func TestVaultManagementUsesSetupRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bootstrap.qa.local.json"), []byte(`{"Repository":"different/fork"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := credentialRepository(root, "qa", "origin/repo"); got != "different/fork" {
		t.Fatal(got)
	}
}
