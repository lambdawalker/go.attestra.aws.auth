package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/nacl/box"
)

type githubClient struct {
	token, base string
	http        *http.Client
}

func newGitHubClient(token string) *githubClient {
	return &githubClient{token: token, base: "https://api.github.com", http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (g *githubClient) request(method, path string, body any, result any) (bool, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return false, err
		}
	}
	req, err := http.NewRequest(method, g.base+path, bytes.NewReader(data))
	if err != nil {
		return false, errors.New("could not prepare GitHub request")
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("Content-Type", "application/json")
	res, err := g.http.Do(req)
	if err != nil {
		return false, errors.New("GitHub request failed; check connectivity (credentials are not printed)")
	}
	defer res.Body.Close()
	if res.StatusCode == 404 && method == http.MethodGet {
		return false, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Do not echo API bodies: failed variable writes can include their values.
		return false, fmt.Errorf("GitHub %s %s returned HTTP %d; check token permissions and repository access", method, path, res.StatusCode)
	}
	if result != nil && res.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(result); err != nil {
			return false, errors.New("invalid GitHub API response")
		}
	}
	return true, nil
}
func environmentPath(repo string) string { return "/repos/" + repo + "/environments/dev" }

type environmentSnapshot struct {
	Exists       bool
	Variables    map[string]string
	SecretExists bool
}

var environmentVariables = []string{"AWS_ACCOUNT_ID", "AWS_ROLE_ARN", "AWS_REGION", "PULUMI_BACKEND_URL", "PULUMI_STACK"}

func (g *githubClient) inspectEnvironment(repo string) (environmentSnapshot, error) {
	s := environmentSnapshot{Variables: map[string]string{}}
	path := environmentPath(repo)
	exists, err := g.request("GET", path, nil, nil)
	if err != nil {
		return s, err
	}
	s.Exists = exists
	if !exists {
		return s, nil
	}
	for _, name := range environmentVariables {
		var v struct{ Value string }
		found, err := g.request("GET", path+"/variables/"+name, nil, &v)
		if err != nil {
			return s, err
		}
		if found {
			s.Variables[name] = v.Value
		}
	}
	s.SecretExists, err = g.request("GET", path+"/secrets/PULUMI_CONFIG_PASSPHRASE", nil, nil)
	return s, err
}
func sealGitHubSecret(publicKey, secret string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(raw) != 32 {
		return "", errors.New("invalid GitHub environment public key")
	}
	var key [32]byte
	copy(key[:], raw)
	encrypted, err := box.SealAnonymous(nil, []byte(secret), &key, rand.Reader)
	if err != nil {
		return "", errors.New("could not encrypt environment secret")
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// Returns names already saved if a later request fails. Never retries writes or
// attempts a destructive rollback of a partially configured environment.
func (g *githubClient) saveEnvironment(repo string, previous environmentSnapshot, values map[string]string, passphrase string) (saved []string, err error) {
	path := environmentPath(repo)
	if !previous.Exists {
		_, err = g.request("PUT", path, map[string]any{"deployment_branch_policy": map[string]bool{"protected_branches": false, "custom_branch_policies": true}}, nil)
		if err != nil {
			return
		}
		saved = append(saved, "dev environment")
		_, err = g.request("POST", path+"/deployment-branch-policies", map[string]string{"name": "main", "type": "branch"}, nil)
		if err != nil {
			return
		}
		saved = append(saved, "main branch restriction")
	}
	for _, name := range environmentVariables {
		old, exists := previous.Variables[name]
		if exists && old == values[name] {
			continue
		}
		method, endpoint := "POST", path+"/variables"
		if exists {
			method = "PATCH"
			endpoint += "/" + url.PathEscape(name)
		}
		_, err = g.request(method, endpoint, map[string]string{"name": name, "value": values[name]}, nil)
		if err != nil {
			return
		}
		saved = append(saved, name)
	}
	if passphrase != "" {
		var key struct {
			Key   string
			KeyID string `json:"key_id"`
		}
		var found bool
		found, err = g.request("GET", path+"/secrets/public-key", nil, &key)
		if err != nil {
			return
		}
		if !found || key.KeyID == "" {
			err = errors.New("environment encryption key unavailable")
			return
		}
		var encrypted string
		encrypted, err = sealGitHubSecret(key.Key, passphrase)
		if err != nil {
			return
		}
		_, err = g.request("PUT", path+"/secrets/PULUMI_CONFIG_PASSPHRASE", map[string]string{"key_id": key.KeyID, "encrypted_value": encrypted}, nil)
		if err != nil {
			return
		}
		saved = append(saved, "PULUMI_CONFIG_PASSPHRASE")
	}
	// Verify public values and secret presence; GitHub never returns secret values.
	var after environmentSnapshot
	after, err = g.inspectEnvironment(repo)
	if err != nil {
		return
	}
	for _, name := range environmentVariables {
		if after.Variables[name] != values[name] {
			err = fmt.Errorf("verification failed for %s", name)
			return
		}
	}
	if !after.SecretExists {
		err = errors.New("passphrase secret presence could not be verified")
	}
	return
}
