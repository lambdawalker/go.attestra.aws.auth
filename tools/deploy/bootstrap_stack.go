package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func stackListed(data []byte, name string) (bool, error) {
	var stacks []struct{ Name string }
	if err := json.Unmarshal(data, &stacks); err != nil || stacks == nil {
		return false, errors.New("invalid Pulumi stack list; no stack initialized")
	}
	for _, s := range stacks {
		if s.Name == "" {
			return false, errors.New("invalid Pulumi stack entry; no stack initialized")
		}
		if s.Name == name || strings.HasSuffix(s.Name, "/attestra-auth-email/"+name) {
			return true, nil
		}
	}
	return false, nil
}

// Only an authoritative successful list can authorize initialization. Failures
// (including permission errors) never become an empty destination stack.
func bootstrapStack(r commandRunner, o options, defaults map[string]string, knownStack bool, setSecret func(string) error) error {
	infra := filepath.Join(o.Root, "infra")
	data, err := r.Exec(infra, true, "pulumi", "stack", "ls", "--json", "--non-interactive")
	if err != nil {
		return errors.New("cannot list backend stacks; check access before retrying; no stack initialized")
	}
	exists, err := stackListed(data, o.Stack)
	if err != nil {
		return err
	}
	if !exists {
		path := filepath.Join(infra, "Pulumi."+o.Stack+".yaml")
		backups, e := filepath.Glob(path + ".bak.*")
		if e != nil {
			return e
		}
		_, statErr := os.Stat(path)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if knownStack || statErr == nil || len(backups) > 0 {
			return fmt.Errorf("stack %s is missing from %s, but local configuration, a backup, or setup history indicates previous setup; no stack initialized and no configuration changed. Select its original backend or migrate/restore its existing state. If that environment was deliberately destroyed, archive its old YAML, backups, and bootstrap checkpoint before starting fresh", o.Stack, o.Backend)
		}
		fmt.Printf("Initializing new %s stack in %s. No previous local stack configuration or setup history found.\n", o.Stack, o.Backend)
		if _, err = r.Exec(infra, false, "pulumi", "stack", "init", o.Stack, "--secrets-provider", "passphrase", "--non-interactive"); err != nil {
			return fmt.Errorf("stack initialization failed; keep any YAML backup and inspect backend before retrying: %w", err)
		}
	} else {
		if _, err = r.Exec(infra, true, "pulumi", "stack", "select", o.Stack, "--non-interactive"); err != nil {
			return err
		}
	}
	data, err = r.Exec(infra, true, "pulumi", "stack", "export", "--show-secrets", "--stack", o.Stack)
	if err != nil {
		return err
	}
	var state struct {
		Deployment struct {
			SecretsProviders struct{ Type string } `json:"secrets_providers"`
			Resources        []json.RawMessage     `json:"resources"`
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment.SecretsProviders.Type != "passphrase" {
		return errors.New("stack must use passphrase encryption; migrate existing state first")
	}
	// Capture only in memory, never echo the decrypted config or put it in a file.
	data, err = r.Exec(infra, true, "pulumi", "config", "--json", "--show-secrets", "--stack", o.Stack, "--non-interactive")
	if err != nil {
		return errors.New("cannot decrypt stack configuration; use the original passphrase and matching local YAML; no configuration overwritten")
	}
	var current map[string]struct {
		Value  string
		Secret bool
	}
	if json.Unmarshal(data, &current) != nil {
		return errors.New("invalid Pulumi configuration response")
	}
	if v, ok := current["aws:region"]; ok && v.Value != o.Region {
		return errors.New("existing stack region differs; select its original region")
	}
	for key := range current {
		if strings.HasPrefix(key, "aws:") && key != "aws:region" {
			return fmt.Errorf("existing provider setting %s requires manual review before bootstrap; nothing overwritten", key)
		}
	}
	if _, ok := current["attestra-auth-email:proofKey"]; exists && len(state.Deployment.Resources) > 0 && !ok {
		return errors.New("existing stack has resources but proofKey is missing from local configuration; restore its matching YAML/config before resuming; no new key generated")
	}
	// Validate existing key before making changes; never silently rotate it.
	if v, ok := current["attestra-auth-email:proofKey"]; ok {
		raw, e := base64.StdEncoding.DecodeString(v.Value)
		if e != nil || len(raw) < 32 || !v.Secret {
			return errors.New("existing proofKey must be an encrypted base64 secret with at least 32 bytes; repair it deliberately")
		}
	}
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, ok := current[key]; ok {
			fmt.Println("Keeping existing", key)
			continue
		}
		if _, err = r.Exec(infra, false, "pulumi", "config", "set", "--stack", o.Stack, "--non-interactive", key, "--", defaults[key]); err != nil {
			return err
		}
	}
	if _, ok := current["attestra-auth-email:proofKey"]; !ok {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		if err = setSecret(base64.StdEncoding.EncodeToString(raw)); err != nil {
			return fmt.Errorf("proofKey not saved; rerun with the same stack passphrase: %w", err)
		}
		fmt.Println("✓ Encrypted proofKey saved (value hidden).")
	}
	return nil
}
