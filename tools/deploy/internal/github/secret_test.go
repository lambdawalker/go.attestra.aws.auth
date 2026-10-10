package github

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestSecretEncryption(t *testing.T) {
	public, private, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := SealSecret(base64.StdEncoding.EncodeToString(public[:]), "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plain, ok := box.OpenAnonymous(nil, raw, public, private)
	if !ok || string(plain) != "passphrase" {
		t.Fatal("not a libsodium-compatible sealed box")
	}
	if _, err := SealSecret("invalid", "secret"); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
