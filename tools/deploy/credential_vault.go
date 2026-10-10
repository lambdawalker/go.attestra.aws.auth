package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

type savedCredentials struct {
	IndexPulumi string
	GitHub      string
	Cloudflare  string
	Pulumi      string
	AWSAccess   string
	AWSSecret   string
	AWSToken    string
}
type credentialVault struct {
	path, scope string
	key, salt   []byte
}
type vaultEnvelope struct {
	Version                 int
	Salt, Nonce, Ciphertext []byte
}

const vaultLimit = 64 * 1024

func validateVaultPassphrase(value string) error {
	value = norm.NFC.String(value)
	count := utf8.RuneCountInString(value)
	if count < 20 || count > 1024 {
		return errors.New("use 20–1024 characters; five or more randomly chosen words are recommended")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("avoid leading or trailing whitespace; spaces between words are welcome")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("use printable characters and spaces")
		}
	}
	lower := strings.ToLower(value)
	for _, weak := range []string{"correct horse battery staple", "this is my password", "this is my passphrase", "password123", "qwertyuiop", "1234567890", "abcdefghijklmnopqrst", "attestra"} {
		if strings.Contains(lower, weak) {
			return errors.New("that passphrase contains a common example or predictable phrase; choose unrelated random words")
		}
	}
	runes := []rune(lower)
	for period := 1; period <= 8; period++ {
		repeated := true
		for i := period; i < len(runes); i++ {
			if runes[i] != runes[i%period] {
				repeated = false
				break
			}
		}
		if repeated {
			return errors.New("avoid repeated characters or repeated short patterns")
		}
	}
	return nil
}
func vaultKey(passphrase string, salt []byte) []byte {
	// Fixed version-1 parameters prevent an edited file requesting unbounded KDF work.
	return argon2.IDKey([]byte(norm.NFC.String(passphrase)), salt, 3, 64*1024, 1, 32)
}
func vaultCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func vaultLocation(repo, environment string) (string, string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	scope := "attestra-credentials-v1\x00" + repo + "\x00" + environment
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(scope)))
	return filepath.Join(dir, "attestra", "credentials", id+".vault.enc"), scope, nil
}
func createVault(path, scope, passphrase string) (*credentialVault, error) {
	if err := validateVaultPassphrase(passphrase); err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return &credentialVault{path: path, scope: scope, salt: salt, key: vaultKey(passphrase, salt)}, nil
}
func unlockVault(path, scope, passphrase string) (*credentialVault, savedCredentials, error) {
	var values savedCredentials
	file, err := os.Open(path)
	if err != nil {
		return nil, values, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, values, err
	}
	if !info.Mode().IsRegular() || info.Size() > vaultLimit {
		return nil, values, errors.New("invalid credential vault file")
	}
	data := make([]byte, info.Size())
	// Read the bounded file fully; truncated/corrupt files are rejected below.
	n, err := io.ReadFull(file, data)
	if err != nil || n != len(data) {
		return nil, values, errors.New("cannot read credential vault")
	}
	var envelope vaultEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 || len(envelope.Salt) != 16 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 {
		return nil, values, errors.New("invalid or unsupported credential vault")
	}
	key := vaultKey(passphrase, envelope.Salt)
	aead, err := vaultCipher(key)
	if err != nil {
		clear(key)
		return nil, values, err
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(scope))
	if err != nil {
		clear(key)
		return nil, values, errors.New("could not unlock credentials: incorrect passphrase, changed vault, or different repository/environment")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &values) != nil {
		clear(key)
		return nil, savedCredentials{}, errors.New("invalid credential vault contents")
	}
	return &credentialVault{path: path, scope: scope, key: key, salt: envelope.Salt}, values, nil
}
func (v *credentialVault) save(values savedCredentials) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return err
	}
	defer clear(plain)
	if len(plain) > vaultLimit/2 {
		return errors.New("credential values exceed vault size limit")
	}
	aead, err := vaultCipher(v.key)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	envelope := vaultEnvelope{Version: 1, Salt: v.salt, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, []byte(v.scope))}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	dir := filepath.Dir(v.path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".vault-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	// Same-directory replacement: failure leaves the previous vault intact.
	if err = os.Rename(temp.Name(), v.path); err != nil {
		return fmt.Errorf("could not replace encrypted vault; previous file retained: %w", err)
	}
	return nil
}
