package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

const testVaultPhrase = "otter meadow lantern cobalt nebula"

func TestCredentialVaultRoundTripAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "dev.json")
	vault, err := createVault(path, "owner/repo:dev", testVaultPhrase)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(vault.key)
	values := savedCredentials{GitHub: "github-private-token", Cloudflare: "cloudflare-private-token", Pulumi: "pulumi-private-passphrase", AWSAccess: "aws-private-access", AWSSecret: "aws-private-secret", AWSToken: "aws-private-session"}
	if err = vault.save(values); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{values.GitHub, values.Cloudflare, values.Pulumi, values.AWSAccess, values.AWSSecret, values.AWSToken, testVaultPhrase} {
		if bytes.Contains(first, []byte(secret)) {
			t.Fatal("plaintext credential written")
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("vault permissions", info.Mode())
		}
	}
	unlocked, got, err := unlockVault(path, "owner/repo:dev", testVaultPhrase)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(unlocked.key)
	if !reflect.DeepEqual(got, values) {
		t.Fatal("credentials changed")
	}
	if err = vault.save(values); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("nonce reused")
	}
	values.GitHub = "replacement-token"
	if err = unlocked.save(values); err != nil {
		t.Fatal(err)
	}
	next, got, err := unlockVault(path, "owner/repo:dev", testVaultPhrase)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(next.key)
	if got.GitHub != values.GitHub {
		t.Fatal("replacement not saved")
	}
}
func TestCredentialVaultRejectsWrongPasswordScopeAndTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.json")
	vault, err := createVault(path, "dev", testVaultPhrase)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(vault.key)
	if err = vault.save(savedCredentials{GitHub: "secret"}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, scope, phrase string }{{"password", "dev", "incorrect secret phrase"}, {"environment", "qa", testVaultPhrase}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, values, err := unlockVault(path, tc.scope, tc.phrase); err == nil || values.GitHub != "" {
				t.Fatal("invalid unlock accepted")
			}
		})
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("failed unlock modified vault")
	}
	var envelope vaultEnvelope
	if err = json.Unmarshal(original, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Ciphertext[0] ^= 1
	changed, _ := json.Marshal(envelope)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = unlockVault(path, "dev", testVaultPhrase); err == nil {
		t.Fatal("tampering accepted")
	}
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{"Version":99}`), bytes.Repeat([]byte("x"), vaultLimit+1)} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err = unlockVault(path, "dev", testVaultPhrase); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
}
func TestCredentialVaultPassphraseValidation(t *testing.T) {
	for _, value := range []string{"short", "aaaaaaaaaaaaaaaaaaaaa", "correct horse battery staple", "password123password123", "12345678901234567890", " lead with enough words but spaces", "long phrase with\ncontrol characters"} {
		if validateVaultPassphrase(value) == nil {
			t.Fatalf("weak phrase accepted: %q", value)
		}
	}
	for _, value := range []string{testVaultPhrase, "árbol nube volcán linterna océano", "qnGsA9VxbT6cmY4pW7zR2H"} {
		if err := validateVaultPassphrase(value); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCredentialVaultScopeIsolation(t *testing.T) {
	a, scopeA, err := vaultLocation("owner/repo", "dev")
	if err != nil {
		t.Fatal(err)
	}
	b, scopeB, err := vaultLocation("owner/repo", "qa")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := vaultLocation("other/repo", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == c || scopeA == scopeB {
		t.Fatal("vault scopes collide")
	}
}
