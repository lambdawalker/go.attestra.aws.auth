package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localSetupFixture(t *testing.T) (*bootstrapWizard, localRevision) {
	t.Helper()
	root := stateTestDir(t)
	git := func(args ...string) {
		t.Helper()
		if _, err := localGit(root, args...); err != nil {
			t.Fatal(args, err)
		}
	}
	git("init")
	git("config", "user.name", "Setup Test")
	git("config", "user.email", "setup@example.com")
	git("remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.Mkdir(filepath.Join(root, "infra"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{".gitignore": "bootstrap.*.local.json*\nteardown.*.local.json*\n", "infra/Pulumi.dev.yaml": "config: {}\n", "app.go": "package main\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-m", "initial")
	w := &bootstrapWizard{root: root, environment: "dev", memory: bootstrapCheckpoint{Repository: "owner/repo", Environment: "dev", Stack: "dev"}}
	revision, err := readLocalRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.recordCompletion(revision, w.configurationDigest()); err != nil {
		t.Fatal(err)
	}
	return w, revision
}
func TestLocalSetupSkipAndInvalidation(t *testing.T) {
	for _, scenario := range []string{"matching", "force", "legacy", "incomplete", "other environment", "other repo", "other origin", "new commit", "settings changed", "config changed", "teardown", "checkpoint backup"} {
		t.Run(scenario, func(t *testing.T) {
			w, revision := localSetupFixture(t)
			force := false
			switch scenario {
			case "force":
				force = true
			case "legacy":
				w.memory.Completion = nil
			case "incomplete":
				w.memory.Stage = "ses-prerequisites"
			case "other environment":
				w.environment = "qa"
			case "other repo":
				w.memory.Repository = "other/repo"
			case "other origin":
				if _, err := localGit(w.root, "remote", "set-url", "origin", "git@github.com:other/repo.git"); err != nil {
					t.Fatal(err)
				}
			case "new commit":
				revision.Commit = strings.Repeat("1", 40)
			case "settings changed":
				w.memory.Backend = "s3://different-state"
			case "config changed":
				if err := os.WriteFile(filepath.Join(w.root, "infra/Pulumi.dev.yaml"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "checkpoint backup":
				if err := os.Rename(w.checkpointPath(), w.checkpointPath()+".bak"); err != nil {
					t.Fatal(err)
				}
			case "teardown":
				if err := os.WriteFile(filepath.Join(w.root, ".attestra", "teardown.dev.local.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := w.locallyComplete("owner/repo", revision, force); got != (scenario == "matching") {
				t.Fatalf("skip=%v", got)
			}
		})
	}
}
func TestLocalSetupDirtyPromptAndNoReceipt(t *testing.T) {
	for _, scenario := range []string{"staged", "unstaged", "untracked"} {
		t.Run(scenario, func(t *testing.T) {
			w, before := localSetupFixture(t)
			name := "app.go"
			if scenario == "untracked" {
				name = "new.go"
			}
			if err := os.WriteFile(filepath.Join(w.root, name), []byte("package changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "staged" {
				if _, err := localGit(w.root, "add", name); err != nil {
					t.Fatal(err)
				}
			}
			cancelled := errors.New("cancelled")
			called := false
			skip, err := w.checkLocalSetup("owner/repo", false, func(message string) error {
				called = true
				if !strings.Contains(message, "uncommitted") {
					t.Fatal(message)
				}
				return cancelled
			})
			if skip || !called || !errors.Is(err, cancelled) {
				t.Fatal(skip, called, err)
			}
			skip, err = w.checkLocalSetup("owner/repo", false, func(string) error { return nil })
			if skip || err != nil {
				t.Fatal(skip, err)
			}
			if err := w.recordCompletion(before, w.configurationDigest()); err != nil {
				t.Fatal(err)
			}
			if w.memory.Completion != nil {
				t.Fatal("dirty deployment received a clean commit receipt")
			}
		})
	}
}
func TestLocalCompletionPersistsAndCommitChangeDuringDeploy(t *testing.T) {
	w, before := localSetupFixture(t)
	resumed := &bootstrapWizard{root: w.root, environment: "dev"}
	if err := resumed.loadSelections("owner/repo", map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
	if !resumed.locallyComplete("owner/repo", before, false) {
		t.Fatal("receipt did not survive rerun")
	}
	if _, err := localGit(w.root, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "changed during deployment"); err != nil {
		t.Fatal(err)
	}
	if err := w.recordCompletion(before, w.configurationDigest()); err != nil {
		t.Fatal(err)
	}
	if w.memory.Completion != nil {
		t.Fatal("commit changed during deployment")
	}
}
func TestLocalEnvironmentDiscovery(t *testing.T) {
	w, _ := localSetupFixture(t)
	for _, name := range []string{".attestra/bootstrap.demo-2.local.json.bak", "infra/Pulumi.qa.yaml", "infra/Pulumi.BAD.yaml"} {
		if err := os.WriteFile(filepath.Join(w.root, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(localEnvironments(w.root), ","); got != "demo-2,dev,qa" {
		t.Fatal(got)
	}
	for _, remote := range []string{"https://github.com/owner/repo.git", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo", "https://github.com/owner/repo"} {
		if _, err := localGit(w.root, "remote", "set-url", "origin", remote); err != nil {
			t.Fatal(err)
		}
		if got := localRepository(w.root); got != "owner/repo" {
			t.Fatal(remote, got)
		}
	}
}
