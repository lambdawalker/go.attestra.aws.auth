package vault

import (
	"path/filepath"
	"testing"
)

func TestVaultPassphraseRotationPreservesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	old := "violet lantern glacier copper meadow"
	next := "silver orchard thunder velvet comet"
	v, e := Create(path, "scope", old)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(v.key)
	values := Credentials{GitHub: "github-value", Cloudflare: "cf-value", Pulumi: "pulumi-value"}
	if e = v.Save(values); e != nil {
		t.Fatal(e)
	}
	rotated, e := RotatePassphrase(v, values, next)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(rotated.key)
	if _, _, e = Unlock(path, "scope", old); e == nil {
		t.Fatal("old passphrase still unlocks")
	}
	unlocked, got, e := Unlock(path, "scope", next)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(unlocked.key)
	if got != values {
		t.Fatal("rotation changed stored credentials")
	}
	if _, e = RotatePassphrase(rotated, values, "weak"); e == nil {
		t.Fatal("weak passphrase accepted")
	}
}
