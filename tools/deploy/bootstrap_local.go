package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A local receipt is evidence of a completed wizard run, not a remote health check.
type setupCompletion struct {
	Commit        string
	CompletedAt   string
	Configuration string
	Checkpoint    string
}
type localRevision struct {
	Commit string
	Dirty  bool
}

func localGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	data, err := cmd.Output()
	return strings.TrimSpace(string(data)), err
}
func readLocalRevision(root string) (localRevision, error) {
	commit, err := localGit(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return localRevision{}, err
	}
	status, err := localGit(root, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return localRevision{}, err
	}
	return localRevision{Commit: commit, Dirty: status != ""}, nil
}
func localRepository(root string) string {
	remote, err := localGit(root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?$`).FindStringSubmatch(remote)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}
func localEnvironments(root string) []string {
	var names []string
	for _, pattern := range []string{"bootstrap.*.local.json*", "infra/Pulumi.*.yaml"} {
		paths, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range paths {
			name := filepath.Base(path)
			if strings.HasPrefix(name, "bootstrap.") {
				name = strings.SplitN(strings.TrimPrefix(name, "bootstrap."), ".local.json", 2)[0]
			} else {
				name = strings.TrimSuffix(strings.TrimPrefix(name, "Pulumi."), ".yaml")
			}
			if validateEnvironment(name) == nil && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}
func (w *bootstrapWizard) configurationDigest() string {
	data, err := os.ReadFile(filepath.Join(w.root, "infra", "Pulumi."+w.environment+".yaml"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func (w *bootstrapWizard) checkpointDigest() string {
	saved := w.memory
	saved.Completion = nil
	data, _ := json.Marshal(saved)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func (w *bootstrapWizard) locallyComplete(repo string, revision localRevision, force bool) bool {
	pending, e := filepath.Glob(indexPendingPath(w.root, w.environment) + "*")
	if e != nil || len(pending) > 0 {
		return false
	}
	receipt := w.memory.Completion
	if force || revision.Dirty || revision.Commit == "" || receipt == nil || w.memory.Stage != "complete" ||
		w.memory.Repository != repo || localRepository(w.root) != repo || w.memory.Environment != w.environment ||
		w.memory.Stack != w.environment || receipt.Commit != revision.Commit || receipt.Checkpoint != w.checkpointDigest() {
		return false
	}
	// Recovering an interrupted checkpoint write must never reuse an older receipt.
	for _, suffix := range []string{".tmp", ".bak"} {
		if _, err := os.Stat(w.checkpointPath() + suffix); !os.IsNotExist(err) {
			return false
		}
	}
	// A local teardown plan, including an interrupted teardown, invalidates assumptions.
	plans, err := filepath.Glob(filepath.Join(w.root, "teardown."+w.environment+".local.json*"))
	if err != nil || len(plans) != 0 {
		return false
	}
	digest := w.configurationDigest()
	return digest != "" && receipt.Configuration == digest
}
func (w *bootstrapWizard) checkLocalSetup(repo string, force bool, approve func(string) error) (bool, error) {
	revision, err := readLocalRevision(w.root)
	if err != nil {
		fmt.Println("Local Git revision could not be checked; continuing with normal setup.")
		return false, nil
	}
	if revision.Dirty {
		if err := approve("Your working tree has uncommitted changes (staged, unstaged, or untracked). Setup will build and deploy these local changes. Continue?"); err != nil {
			return false, err
		}
	}
	if !w.locallyComplete(repo, revision, force) {
		return false, nil
	}
	fmt.Printf("✓ %s is all set according to the local setup record.\nCommit: %s\nCompleted: %s\n", w.environment, revision.Commit, w.memory.Completion.CompletedAt)
	fmt.Println("No AWS, GitHub, or Cloudflare checks were made. Changes made outside this checkout are not detected. Run setup with -force-setup to check and reconcile remote configuration.")
	return true, nil
}
func (w *bootstrapWizard) recordCompletion(before localRevision, configuration string) error {
	w.memory.StackInitialized = true
	w.memory.Stage = "complete"
	w.memory.Completion = nil
	after, err := readLocalRevision(w.root)
	// Never associate dirty code or edits made while deploying with a clean commit.
	if err == nil && before.Commit != "" && !before.Dirty && before == after && configuration != "" && configuration == w.configurationDigest() {
		w.memory.Completion = &setupCompletion{Commit: after.Commit, CompletedAt: time.Now().UTC().Format(time.RFC3339), Configuration: w.configurationDigest(), Checkpoint: w.checkpointDigest()}
	} else {
		fmt.Println("Setup completed, but no unchanged-commit shortcut was saved: commit local changes and rerun setup from a clean checkout to enable it.")
	}
	return w.saveMemory()
}
