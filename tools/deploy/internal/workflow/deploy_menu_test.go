package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeMenuCheckpoint(t *testing.T, root, name, stage, repo string) {
	t.Helper()
	os.MkdirAll(filepath.Join(root, ".attestra"), 0700)
	data, _ := json.Marshal(bootstrapCheckpoint{Repository: repo, Environment: name, Stack: name, Stage: stage})
	if err := os.WriteFile(filepath.Join(root, ".attestra", "bootstrap."+name+".local.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestDeploymentStartupFirstRunAndMenu(t *testing.T) {
	for _, tc := range []struct {
		name, stage, repo, want string
		flags                   []string
		wantPrompt              bool
	}{
		{name: "fresh", want: "setup", flags: []string{"repo-root"}},
		{name: "incomplete", stage: "deploying", repo: "owner/repo", want: "setup"},
		{name: "complete", stage: "complete", repo: "owner/repo", want: "both", wantPrompt: true},
		{name: "setup launcher", stage: "complete", repo: "owner/repo", want: "both", wantPrompt: true, flags: []string{"repo-root", "bootstrap"}},
		{name: "different repo", stage: "complete", repo: "other/repo", want: "setup"},
		{name: "explicit stack", stage: "complete", repo: "owner/repo", want: "explicit", flags: []string{"stack"}},
		{name: "explicit scope", want: "explicit", flags: []string{"scope"}},
		{name: "forced setup", want: "explicit", flags: []string{"bootstrap", "force-setup"}},
		{name: "ci", want: "explicit", flags: []string{"ci"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.stage != "" {
				writeMenuCheckpoint(t, root, "dev", tc.stage, tc.repo)
			}
			prompted := false
			got, err := deploymentStartup(root, "owner/repo", tc.flags, func() (string, error) { prompted = true; return "both", nil })
			if err != nil || got != tc.want || prompted != tc.wantPrompt {
				t.Fatalf("%q %v prompted=%v", got, err, prompted)
			}
		})
	}
}
func TestDeploymentMenuCancellationAndCorruptReceipt(t *testing.T) {
	root := t.TempDir()
	writeMenuCheckpoint(t, root, "dev", "complete", "owner/repo")
	cancel := errors.New("cancelled")
	if _, err := deploymentStartup(root, "owner/repo", nil, func() (string, error) { return "", cancel }); !errors.Is(err, cancel) {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".attestra", "bootstrap.dev.local.json"), []byte("broken"), 0600)
	if _, err := deploymentStartup(root, "owner/repo", nil, func() (string, error) { t.Fatal("prompted on corrupt receipt"); return "app", nil }); err == nil {
		t.Fatal("ignored corrupt receipt")
	}
}
func TestBothDeploymentsRunSharedFirstAndStopOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var calls []string
		err := runDeploymentChoice("both", func() error {
			calls = append(calls, "shared")
			if fail {
				return errors.New("failed")
			}
			return nil
		}, func() error { calls = append(calls, "app"); return nil })
		want := []string{"shared", "app"}
		if fail {
			want = want[:1]
		}
		if !reflect.DeepEqual(calls, want) || (err != nil) != fail {
			t.Fatalf("%v %v", calls, err)
		}
	}
}

func TestSingleDeploymentChoicesDoNotRunOtherScope(t *testing.T) {
	for _, scope := range []string{"app", "shared"} {
		var calls []string
		if err := runDeploymentChoice(scope, func() error { calls = append(calls, "shared"); return nil }, func() error { calls = append(calls, "app"); return nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []string{scope}) {
			t.Fatal(calls)
		}
	}
}
func TestBothScopeRejectsUnrelatedOperations(t *testing.T) {
	if err := validateDeployScope("both", []string{"scope", "stack", "region", "profile", "sso"}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"bootstrap", "ci", "publish-index", "release-index-lock", "health-check", "manage-credentials"} {
		if validateDeployScope("both", []string{flag}) == nil {
			t.Fatalf("accepted %s", flag)
		}
	}
}
func TestMenuRecognizesCompletedSetupWithoutCommitReceipt(t *testing.T) {
	root := t.TempDir()
	writeMenuCheckpoint(t, root, "qa", "complete", "owner/repo")
	got, err := deploymentStartup(root, "owner/repo", nil, func() (string, error) { return "app", nil })
	if err != nil || got != "app" {
		t.Fatalf("%s %v", got, err)
	}
}
