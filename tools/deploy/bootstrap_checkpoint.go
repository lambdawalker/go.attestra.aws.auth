package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Only non-secret selections are persisted, so interrupted bootstrap can reuse
// its bucket before a GitHub environment exists. Never store a token/passphrase.
type bootstrapCheckpoint struct {
	Repository  string
	Environment string
	Region      string
	Backend     string
	Stack       string
}

func (w *bootstrapWizard) loadSelections(repo string, values map[string]string, existing map[string]string) error {
	path := w.checkpointPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && w.environment == "dev" {
		data, err = os.ReadFile(filepath.Join(w.root, "bootstrap.local.json"))
	}
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved bootstrapCheckpoint
	if json.Unmarshal(data, &saved) != nil {
		return errors.New("invalid bootstrap checkpoint; inspect it before resuming")
	}
	if saved.Repository != repo || (saved.Environment != "" && saved.Environment != w.environment) || saved.Stack != w.environment {
		return nil
	}
	for key, value := range map[string]string{"AWS_REGION": saved.Region, "PULUMI_BACKEND_URL": saved.Backend, "PULUMI_STACK": saved.Stack} {
		if existing[key] == "" && value != "" {
			values[key] = value
		}
	}
	return nil
}
func (w *bootstrapWizard) saveSelections(repo string, values map[string]string) error {
	data, err := json.MarshalIndent(bootstrapCheckpoint{Environment: w.environment, Repository: repo, Region: values["AWS_REGION"], Backend: values["PULUMI_BACKEND_URL"], Stack: values["PULUMI_STACK"]}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.checkpointPath(), append(data, '\n'), 0600)
}

func (w *bootstrapWizard) checkpointPath() string {
	return filepath.Join(w.root, "bootstrap."+w.environment+".local.json")
}
