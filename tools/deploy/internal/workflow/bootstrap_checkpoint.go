package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Only non-secret selections are persisted, so interrupted bootstrap can reuse
// its bucket before a GitHub environment exists. Never store a token/passphrase.
type bootstrapCheckpoint struct {
	Summary          setupSummaryState `json:",omitempty"`
	StackInitialized bool              `json:",omitempty"`
	Completion       *setupCompletion  `json:",omitempty"`
	Repository       string
	Environment      string
	Region           string
	Backend          string
	Stack            string
	Settings         map[string]string
	Stage            string
	Account          string
	RoleARN          string
}

func (w *bootstrapWizard) loadSelections(repo string, values map[string]string, existing map[string]string) error {
	path := w.checkpointPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data, err = os.ReadFile(path + ".bak")
	}
	if os.IsNotExist(err) && w.environment == "dev" {
		data, err = os.ReadFile(filepath.Join(w.root, ".attestra", "bootstrap.local.json"))
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
	// Preserve evidence from checkpoints written before StackInitialized existed.
	switch saved.Stage {
	case "github-ready", "deploying", "ses-prerequisites", "ses-verified", "deployed", "complete":
		saved.StackInitialized = true
	}
	if saved.Completion != nil {
		saved.StackInitialized = true
	}
	if saved.StackInitialized && existing["PULUMI_BACKEND_URL"] != "" && saved.Backend != "" && existing["PULUMI_BACKEND_URL"] != saved.Backend {
		return errors.New("GitHub backend differs from the previously initialized stack backend; migrate or verify its existing state before changing the local checkpoint")
	}
	w.memory = saved
	w.resuming = true
	for key, value := range map[string]string{"AWS_REGION": saved.Region, "PULUMI_BACKEND_URL": saved.Backend, "PULUMI_STACK": saved.Stack, "AWS_ACCOUNT_ID": saved.Account, "AWS_ROLE_ARN": saved.RoleARN} {
		if existing[key] == "" && value != "" {
			values[key] = value
		}
	}
	return nil
}
func (w *bootstrapWizard) saveSelections(repo string, values map[string]string) error {
	w.memory.Repository, w.memory.Environment = repo, w.environment
	w.memory.Region, w.memory.Backend, w.memory.Stack = values["AWS_REGION"], values["PULUMI_BACKEND_URL"], values["PULUMI_STACK"]
	w.memory.Account, w.memory.RoleARN = values["AWS_ACCOUNT_ID"], values["AWS_ROLE_ARN"]
	return w.saveMemory()
}
func (w *bootstrapWizard) saveMemory() error {
	data, err := json.MarshalIndent(w.memory, "", "  ")
	if err != nil {
		return err
	}
	path := w.checkpointPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0600); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".bak"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	_ = os.Remove(path + ".bak")
	return nil
}

func (w *bootstrapWizard) checkpointPath() string {
	return filepath.Join(w.root, ".attestra", "bootstrap."+w.environment+".local.json")
}

// Only explicitly allowlisted non-secret UI settings enter the checkpoint.
func (w *bootstrapWizard) remember(key, value string) error {
	switch key {
	case "authMode", "profile", "baseDomain", "origin", "senderDomain", "sender", "roleName", "route53", "dnsProvider", "immutable", "dnsFingerprint":
	default:
		return errors.New("setting is not allowed in bootstrap memory")
	}
	if w.memory.Settings == nil {
		w.memory.Settings = map[string]string{}
	}
	w.memory.Settings[key] = value
	return w.saveMemory()
}
func (w *bootstrapWizard) remembered(key string) (string, bool) {
	if w == nil {
		return "", false
	}
	v, ok := w.memory.Settings[key]
	return v, ok
}
func (w *bootstrapWizard) markStage(stage string) error {
	w.memory.Stage = stage
	if stage == "setup-in-progress" {
		w.memory.Summary.Steps = nil
	}
	if stage == "deploying" {
		for _, key := range []string{"prerequisites", "verification", "application", "publication", "health"} {
			delete(w.memory.Summary.Steps, key)
		}
	}
	key := map[string]string{"github-ready": "github", "ses-prerequisites": "prerequisites", "ses-verified": "verification", "deployed": "application"}[stage]
	if key != "" {
		if w.memory.Summary.Steps == nil {
			w.memory.Summary.Steps = map[string]bool{}
		}
		w.memory.Summary.Steps[key] = true
	}
	return w.saveMemory()
}
