package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
)

func TestFastNukeCheckOnlyRechecksLatestRemainingArtifacts(t *testing.T) {
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{
		Account: "123456789012", CheckedAt: "old-check",
		Results: []nukeResult{{"State bucket removed", "RETAINED", "old nuke"}},
		CheckResults: []nukeResult{
			{"State bucket removed", "ABSENT", "previous check"},
			{"State bucket remaining", "REMAINS", "previous check"},
			{"State bucket unknown", "UNVERIFIED", "permission denied"},
		},
		Backends: []nukeBackend{{"removed", "us-east-2"}, {"remaining", "us-east-2"}, {"unknown", "us-east-2"}},
	}}
	calls := 0
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(_ any, args ...string) (bool, error) {
			calls++
			if args[0] != "s3api" || args[1] != "head-bucket" || args[3] != "remaining" {
				t.Fatalf("checked an excluded artifact: %v", args)
			}
			return false, &awsenv.Error{Code: "NoSuchBucket"}
		}}
	}
	if n.checkLastNukeMode(true) == nil {
		t.Fatal("skipped UNVERIFIED evidence must not become overall success")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if len(n.receipt.CheckResults) != 3 || n.receipt.CheckResults[0].Detail != "previous check" || n.receipt.CheckResults[1].Status != "ABSENT" || n.receipt.CheckResults[2].Status != "UNVERIFIED" {
		t.Fatal(n.receipt.CheckResults)
	}
	if n.receipt.CheckScope != "remaining-only" || len(n.receipt.CheckedArtifacts) != 1 {
		t.Fatal("missing scope metadata")
	}
	data, err := os.ReadFile(n.path)
	var saved nukeReceipt
	if err != nil || json.Unmarshal(data, &saved) != nil || len(saved.CheckResults) != 3 {
		t.Fatal("snapshot not saved", err)
	}
	if n.checkLastNukeMode(true) == nil {
		t.Fatal("cached unverified artifact lost")
	}
	if calls != 1 {
		t.Fatal("empty fast check made resource calls")
	}
}

func TestFastNukeCheckRequiresPreviousCheck(t *testing.T) {
	n := &nukeWorkflow{receipt: nukeReceipt{Results: []nukeResult{{"resource", "FAILED", "old nuke"}}}}
	if n.checkLastNukeMode(true) == nil {
		t.Fatal("fast check silently fell back to all artifacts")
	}
}

func TestFastNukeCheckNoRemainingArtifactsSucceedsWithoutCalls(t *testing.T) {
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{CheckedAt: "old-check", CheckResults: []nukeResult{{"gone", "ABSENT", "previous check"}}}}
	n.awsForRegion = func(string) awsenv.API { t.Fatal("unexpected resource probe"); return nil }
	if err := n.checkLastNukeMode(true); err != nil {
		t.Fatal(err)
	}
	if len(n.receipt.CheckedArtifacts) != 0 || len(n.receipt.CheckResults) != 1 {
		t.Fatal(n.receipt)
	}
}

func TestFastNukeCheckCLIRequiresNukeTeardown(t *testing.T) {
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		if validateNukeFlags(flags[0], flags[1], []string{"fast-check"}) == nil {
			t.Fatal("standalone fast check accepted")
		}
	}
	if err := validateNukeFlags(true, true, []string{"nuke", "teardown", "fast-check"}); err != nil {
		t.Fatal(err)
	}
}
