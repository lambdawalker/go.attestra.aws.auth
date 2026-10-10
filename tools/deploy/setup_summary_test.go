package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreviewSummaryCountsOnlyFinalSummary(t *testing.T) {
	counts, err := readPreviewChanges(strings.NewReader("{\"diagnosticEvent\":{\"message\":\"secret\"}}\n{\"summaryEvent\":{\"resourceChanges\":{\"create\":3,\"update\":2,\"same\":4,\"replace\":1,\"create-replacement\":1,\"delete-replaced\":1}}}\n"))
	if err != nil || counts["create"] != 3 || counts["replace"] != 1 {
		t.Fatalf("%v %v", counts, err)
	}
	text := formatResourceChanges(counts)
	if strings.Contains(text, "secret") || !strings.Contains(text, "replace 1") {
		t.Fatal(text)
	}
	for _, bad := range []string{"", `{"diagnosticEvent":{}}`, `{"summaryEvent":{"resourceChanges":{"create":-1}}}`} {
		if _, err := readPreviewChanges(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted missing/invalid summary")
		}
	}
}
func TestSetupSummaryUsesRecordedEvidence(t *testing.T) {
	w := &bootstrapWizard{environment: "qa", memory: bootstrapCheckpoint{Account: "123456789012", Region: "us-east-2", Backend: "s3://state", Settings: map[string]string{"dnsProvider": "cloudflare"}}}
	text := w.setupSummary()
	for _, want := range []string{"qa", "123456789012", "us-east-2", "Cloudflare", "PENDING", "not available"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	w.memory.Summary.Steps = map[string]bool{"state": true, "github": true}
	w.memory.Summary.Domains = map[string]string{"API": "https://qa.api.example.com"}
	text = w.setupSummary()
	if !strings.Contains(text, "DONE     State bucket and Pulumi stack") || !strings.Contains(text, "PENDING  Final health check") || !strings.Contains(text, "qa.api.example.com") {
		t.Fatal(text)
	}
}

func TestPreviewHookCapturesCountsAndDeletesRawLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
[ "$PULUMI_DEBUG_COMMANDS" = "true" ] || exit 4
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--event-log" ]; then shift; log="$1"; fi
 shift
done
printf '%s' "$log" > "$PREVIEW_TEST_PATH"
printf '%s\n' '{"summaryEvent":{"resourceChanges":{"create":2,"same":1}}}' > "$log"
exit "$PREVIEW_TEST_EXIT"
`
	if err := os.WriteFile(filepath.Join(dir, "pulumi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pathRecord := filepath.Join(dir, "path")
	for _, code := range []string{"0", "1"} {
		t.Setenv("PREVIEW_TEST_PATH", pathRecord)
		t.Setenv("PREVIEW_TEST_EXIT", code)
		called := false
		r := &processRunner{onPreview: func(scope string, changes map[string]int, success bool) {
			called = true
			if changes["create"] != 2 || success != (code == "0") {
				t.Errorf("wrong preview result %v %v", changes, success)
			}
		}}
		_, err := r.Exec(dir, true, "pulumi", "preview", "--stack", "qa")
		if !called || (err == nil) != (code == "0") {
			t.Fatalf("callback %v error %v", called, err)
		}
		path, _ := os.ReadFile(pathRecord)
		if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
			t.Fatal("raw preview log retained")
		}
	}
}
func TestSummaryRetryInvalidatesDependentSteps(t *testing.T) {
	w := &bootstrapWizard{root: t.TempDir(), environment: "qa"}
	for _, key := range []string{"state", "github", "application", "publication", "health"} {
		if err := w.summaryStep(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.markStage("deploying"); err != nil {
		t.Fatal(err)
	}
	if !w.memory.Summary.Steps["github"] || w.memory.Summary.Steps["health"] || w.memory.Summary.Steps["application"] {
		t.Fatal("stale completion on retry")
	}
}

func TestSummaryHealthRetryRecordsLatestResult(t *testing.T) {
	w := &bootstrapWizard{root: t.TempDir(), environment: "qa"}
	if err := w.recordSummaryHealth(true); err != nil {
		t.Fatal(err)
	}
	if !w.memory.Summary.Steps["health"] {
		t.Fatal("successful retry still pending")
	}
	w.memory.Stage = "complete"
	w.memory.Completion = &setupCompletion{Commit: "old"}
	if err := w.recordSummaryHealth(false); err != nil {
		t.Fatal(err)
	}
	if w.memory.Summary.Steps["health"] || w.memory.Completion != nil || w.memory.Stage == "complete" {
		t.Fatal("failed health check kept stale completion")
	}
}
