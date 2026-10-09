package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestSetupDefaultsAndValidation(t *testing.T) {
	v := setupDefaults(map[string]string{"AWS_REGION": "us-west-2", "AWS_ROLE_ARN": "arn:aws:iam::123456789012:role/existing"})
	if v["AWS_REGION"] != "us-west-2" || v["AWS_ACCOUNT_ID"] != "123456789012" || v["PULUMI_BACKEND_URL"] != "s3://pulumi-state-1p8322nx" || v["PULUMI_STACK"] != "dev" {
		t.Fatal(v)
	}
	if err := validateSetupValues(v); err != nil {
		t.Fatal(err)
	}
	v["AWS_ACCOUNT_ID"] = "000000000000"
	if validateSetupValues(v) == nil {
		t.Fatal("accepted role in another account")
	}
}
func TestSecretEncryption(t *testing.T) {
	public, private, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := sealGitHubSecret(base64.StdEncoding.EncodeToString(public[:]), "passphrase")
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
	if _, err := sealGitHubSecret("invalid", "secret"); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
func TestSaveEnvironment(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			values := setupDefaults(nil)
			values["AWS_ACCOUNT_ID"] = "123456789012"
			values["AWS_ROLE_ARN"] = "arn:aws:iam::123456789012:role/deploy"
			stored := map[string]string{}
			secret := existing
			exists := existing
			public, private, _ := box.GenerateKey(rand.Reader)
			mutations := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authentication")
				}
				path := strings.TrimPrefix(r.URL.Path, environmentPath("owner/repo"))
				if r.Method != "GET" {
					mutations = append(mutations, r.Method+" "+path)
				}
				if path == "" {
					if r.Method == "PUT" {
						exists = true
						var body map[string]json.RawMessage
						json.NewDecoder(r.Body).Decode(&body)
						if _, ok := body["deployment_branch_policy"]; !ok {
							t.Error("no branch restriction")
						}
					}
					if !exists {
						w.WriteHeader(404)
					} else {
						fmt.Fprint(w, `{}`)
					}
					return
				}
				if path == "/deployment-branch-policies" {
					fmt.Fprint(w, `{}`)
					return
				}
				if path == "/variables" || strings.HasPrefix(path, "/variables/") {
					if r.Method == "GET" {
						v, ok := stored[strings.TrimPrefix(path, "/variables/")]
						if !ok {
							w.WriteHeader(404)
							return
						}
						json.NewEncoder(w).Encode(map[string]string{"value": v})
						return
					}
					var body struct{ Name, Value string }
					json.NewDecoder(r.Body).Decode(&body)
					stored[body.Name] = body.Value
					w.WriteHeader(204)
					return
				}
				if path == "/secrets/public-key" {
					json.NewEncoder(w).Encode(map[string]string{"key": base64.StdEncoding.EncodeToString(public[:]), "key_id": "key-id"})
					return
				}
				if path == "/secrets/PULUMI_CONFIG_PASSPHRASE" {
					if r.Method == "PUT" {
						var body map[string]string
						json.NewDecoder(r.Body).Decode(&body)
						raw, _ := base64.StdEncoding.DecodeString(body["encrypted_value"])
						plain, ok := box.OpenAnonymous(nil, raw, public, private)
						if !ok || string(plain) != "my-passphrase" || body["key_id"] != "key-id" {
							t.Error("secret encryption failed")
						}
						secret = true
					}
					if !secret {
						w.WriteHeader(404)
					} else {
						fmt.Fprint(w, `{}`)
					}
					return
				}
				t.Errorf("unexpected request %s %s", r.Method, path)
				w.WriteHeader(500)
			}))
			defer srv.Close()
			g := newGitHubClient("test-token")
			g.base = srv.URL
			previous := environmentSnapshot{Exists: existing, Variables: map[string]string{}, SecretExists: existing}
			passphrase := "my-passphrase"
			if existing {
				for k, v := range values {
					stored[k] = v
					previous.Variables[k] = v
				}
				stored["AWS_REGION"] = "us-west-1"
				previous.Variables["AWS_REGION"] = "us-west-1"
				passphrase = ""
			}
			_, err := g.saveEnvironment("owner/repo", previous, values, passphrase)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, values) {
				t.Fatal("stored values differ")
			}
			if existing && !reflect.DeepEqual(mutations, []string{"PATCH /variables/AWS_REGION"}) {
				t.Fatal("existing protections/secret altered", mutations)
			}
			if !existing && mutations[0] != "PUT " {
				t.Fatal("environment not created first")
			}
		})
	}
}
func TestSetupFailureDoesNotLeakValues(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(403)
		fmt.Fprint(w, `sensitive-response-value`)
	}))
	defer srv.Close()
	g := newGitHubClient("secret-token")
	g.base = srv.URL
	saved, err := g.saveEnvironment("owner/repo", environmentSnapshot{Variables: map[string]string{}}, setupDefaults(nil), "secret-pass")
	if err == nil || count != 2 || len(saved) != 1 {
		t.Fatal("did not stop after partial failure", saved, err)
	}
	for _, s := range []string{"sensitive-response-value", "secret-token", "secret-pass"} {
		if strings.Contains(err.Error(), s) {
			t.Fatal("leaked credential")
		}
	}
}
