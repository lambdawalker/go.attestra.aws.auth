package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"

	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"
)

func teardownEnvironments(root string) []string {
	names := localEnvironments(root)
	paths, _ := filepath.Glob(filepath.Join(root, "teardown.*.local.json*"))
	for _, path := range paths {
		name := strings.SplitN(strings.TrimPrefix(filepath.Base(path), "teardown."), ".local.json", 2)[0]
		if ui.ValidateEnvironment(name) == nil && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
func cleanupTeardownLocal(root string, p *teardownProgress) error {
	if err := ui.ValidateEnvironment(p.Environment); err != nil {
		return err
	}
	paths := []string{filepath.Join(root, "bootstrap."+p.Environment+".local.json"), filepath.Join(root, "bootstrap."+p.Environment+".local.json.bak")}
	if p.Environment == "dev" {
		paths = append(paths, filepath.Join(root, "bootstrap.local.json"), filepath.Join(root, "bootstrap.local.json.bak"))
	}
	// Validate all local setup records before removing any. Another repository
	// may have used the same checkout/environment name.
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var saved bootstrapCheckpoint
		if json.Unmarshal(data, &saved) != nil || saved.Repository != p.Repository || (saved.Environment != "" && saved.Environment != p.Environment) || (saved.Stack != "" && saved.Stack != p.Environment) {
			return errors.New("local setup record belongs to another repository/environment or is invalid; inspect it before local cleanup")
		}
	}
	paths = append(paths, filepath.Join(root, "android-config", p.Environment+".properties"))
	if p.DeleteVault {
		path, _, err := credentialvault.Location(p.Repository, p.Environment)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func teardownRepository(root, environment string) string {
	var saved teardownProgress
	data, err := os.ReadFile(filepath.Join(root, "teardown."+environment+".local.json"))
	if err == nil && json.Unmarshal(data, &saved) == nil && saved.Environment == environment && saved.Repository != "" {
		return saved.Repository
	}
	return credentialRepository(root, environment, localRepository(root))
}

// Inspect only the selected environment's local receipt before unlocking a
// vault or contacting providers. Keep completed receipts as recreation guards.
func completedTeardown(root, environment string) (bool, error) {
	if err := ui.ValidateEnvironment(environment); err != nil {
		return false, err
	}
	path := filepath.Join(root, "teardown."+environment+".local.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var p teardownProgress
	if json.Unmarshal(data, &p) != nil || p.Environment != environment {
		return false, fmt.Errorf("invalid or mismatched teardown checkpoint for environment %q: %s", environment, path)
	}
	return p.Complete, nil
}

func chooseTeardownEnvironment(root string, choose func() (string, error), another func() (bool, error)) (string, error) {
	for {
		environment, err := choose()
		if err != nil {
			return "", err
		}
		complete, err := completedTeardown(root, environment)
		if err != nil {
			return "", err
		}
		if !complete {
			fmt.Printf("Selected environment: %s\n", environment)
			return environment, nil
		}
		path := filepath.Join(root, "teardown."+environment+".local.json")
		fmt.Printf("Teardown for %q is already complete. Saved progress: %s\nThis receipt applies only to %s. Keep it unless that same environment has been recreated; in that case archive this exact file before retrying.\n", environment, path, environment)
		retry, err := another()
		if err != nil {
			return "", err
		}
		if !retry {
			return "", errors.New("teardown cancelled; no resources changed")
		}
	}
}
