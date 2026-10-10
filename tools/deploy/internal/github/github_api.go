package github

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

type Client struct {
	token, BaseURL string
	http           *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, BaseURL: "https://api.github.com", http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (g *Client) Request(method, path string, body any, result any) (bool, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return false, err
		}
	}
	req, err := http.NewRequest(method, g.BaseURL+path, bytes.NewReader(data))
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
func EnvironmentPath(repo, environment string) string {
	return "/repos/" + repo + "/environments/" + url.PathEscape(environment)
}

type EnvironmentSnapshot struct {
	Exists       bool
	Variables    map[string]string
	SecretExists bool
}

var EnvironmentVariables = []string{"AWS_ACCOUNT_ID", "AWS_ROLE_ARN", "AWS_REGION", "PULUMI_BACKEND_URL", "PULUMI_STACK"}

func (g *Client) InspectEnvironment(repo, environment string) (EnvironmentSnapshot, error) {
	s := EnvironmentSnapshot{Variables: map[string]string{}}
	path := EnvironmentPath(repo, environment)
	exists, err := g.Request("GET", path, nil, nil)
	if err != nil {
		return s, err
	}
	s.Exists = exists
	if !exists {
		return s, nil
	}
	for _, name := range EnvironmentVariables {
		var v struct{ Value string }
		found, err := g.Request("GET", path+"/variables/"+name, nil, &v)
		if err != nil {
			return s, err
		}
		if found {
			s.Variables[name] = v.Value
		}
	}
	s.SecretExists, err = g.Request("GET", path+"/secrets/PULUMI_CONFIG_PASSPHRASE", nil, nil)
	return s, err
}
func SealSecret(publicKey, secret string) (string, error) {
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
func (g *Client) SaveEnvironment(repo, environment string, previous EnvironmentSnapshot, values map[string]string, passphrase string) (saved []string, err error) {
	path := EnvironmentPath(repo, environment)
	if !previous.Exists {
		_, err = g.Request("PUT", path, map[string]any{"deployment_branch_policy": map[string]bool{"protected_branches": false, "custom_branch_policies": true}}, nil)
		if err != nil {
			return
		}
		saved = append(saved, environment+" environment")
		_, err = g.Request("POST", path+"/deployment-branch-policies", map[string]string{"name": "main", "type": "branch"}, nil)
		if err != nil {
			return
		}
		saved = append(saved, "main branch restriction")
	}
	for _, name := range EnvironmentVariables {
		old, exists := previous.Variables[name]
		if exists && old == values[name] {
			continue
		}
		method, endpoint := "POST", path+"/variables"
		if exists {
			method = "PATCH"
			endpoint += "/" + url.PathEscape(name)
		}
		_, err = g.Request(method, endpoint, map[string]string{"name": name, "value": values[name]}, nil)
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
		found, err = g.Request("GET", path+"/secrets/public-key", nil, &key)
		if err != nil {
			return
		}
		if !found || key.KeyID == "" {
			err = errors.New("environment encryption key unavailable")
			return
		}
		var encrypted string
		encrypted, err = SealSecret(key.Key, passphrase)
		if err != nil {
			return
		}
		_, err = g.Request("PUT", path+"/secrets/PULUMI_CONFIG_PASSPHRASE", map[string]string{"key_id": key.KeyID, "encrypted_value": encrypted}, nil)
		if err != nil {
			return
		}
		saved = append(saved, "PULUMI_CONFIG_PASSPHRASE")
	}
	// Verify public values and secret presence; GitHub never returns secret values.
	var after EnvironmentSnapshot
	after, err = g.InspectEnvironment(repo, environment)
	if err != nil {
		return
	}
	for _, name := range EnvironmentVariables {
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

func (g *Client) ListEnvironments(repo string) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		var response struct{ Environments []struct{ Name string } }
		found, err := g.Request("GET", fmt.Sprintf("/repos/%s/environments?per_page=100&page=%d", repo, page), nil, &response)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("cannot list repository environments; check token permissions")
		}
		for _, env := range response.Environments {
			names = append(names, env.Name)
		}
		if len(response.Environments) < 100 {
			return names, nil
		}
	}
}
func (g *Client) EnsureNoDeployments(repo string) error {
	for _, status := range []string{"queued", "in_progress", "waiting", "pending", "requested"} {
		var result struct {
			TotalCount int `json:"total_count"`
		}
		found, err := g.Request("GET", "/repos/"+repo+"/actions/workflows/deploy.yml/runs?per_page=1&status="+status, nil, &result)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("cannot inspect Deploy workflow; no teardown authorized")
		}
		if result.TotalCount > 0 {
			return errors.New("Deploy workflow has active or waiting runs; finish/cancel them before teardown")
		}
	}
	return nil
}
func (g *Client) CheckTeardownSharing(repo, environment string, values map[string]string) error {
	names, err := g.ListEnvironments(repo)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == environment {
			continue
		}
		other, err := g.InspectEnvironment(repo, name)
		if err != nil {
			return err
		}
		if other.Variables["AWS_ROLE_ARN"] == values["AWS_ROLE_ARN"] || (other.Variables["PULUMI_BACKEND_URL"] == values["PULUMI_BACKEND_URL"] && other.Variables["PULUMI_STACK"] == values["PULUMI_STACK"]) {
			return fmt.Errorf("environment %s shares this role or stack; resolve sharing before teardown", name)
		}
	}
	return nil
}
