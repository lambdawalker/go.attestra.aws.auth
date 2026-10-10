package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type previewSummary struct {
	ObservedAt string
	Changes    map[string]int
	Successful bool
}
type setupSummaryState struct {
	Domains  map[string]string         `json:",omitempty"`
	Steps    map[string]bool           `json:",omitempty"`
	Previews map[string]previewSummary `json:",omitempty"`
}

var summarySteps = []struct{ key, label string }{
	{"state", "State bucket and Pulumi stack"}, {"index-infra", "Shared index infrastructure"},
	{"github", "AWS role and GitHub environment"}, {"prerequisites", "SES/ACM prerequisites"},
	{"verification", "DNS verification and HTTPS certificate"}, {"application", "Application deployment and API DNS"},
	{"publication", "Environment index publication"}, {"health", "Final health check"},
}

func readPreviewChanges(r io.Reader) (map[string]int, error) {
	decoder := json.NewDecoder(r)
	var result map[string]int
	for {
		var event struct {
			Summary *struct {
				Changes map[string]int `json:"resourceChanges"`
			} `json:"summaryEvent"`
		}
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return nil, errors.New("preview event log is incomplete")
		}
		if event.Summary != nil {
			result = event.Summary.Changes
		}
	}
	if result == nil {
		return nil, errors.New("preview summary unavailable")
	}
	for _, count := range result {
		if count < 0 {
			return nil, errors.New("invalid preview count")
		}
	}
	return result, nil
}
func formatResourceChanges(changes map[string]int) string {
	parts := []string{}
	for _, key := range []string{"create", "update", "replace", "delete", "same", "read", "import", "refresh"} {
		if n := changes[key]; n > 0 {
			label := key
			if key == "same" {
				label = "unchanged"
			}
			parts = append(parts, fmt.Sprintf("%s %d", label, n))
		}
	}
	// Replacement sub-operations are already represented by "replace".
	extra := []string{}
	for key, n := range changes {
		if n == 0 {
			continue
		}
		switch key {
		case "create", "update", "replace", "delete", "same", "read", "import", "refresh", "create-replacement", "delete-replaced", "read-replacement", "import-replacement":
		default:
			extra = append(extra, fmt.Sprintf("%s %d", key, n))
		}
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return "no resource changes"
	}
	return strings.Join(parts, ", ")
}
func previewScope(dir string, args []string) string {
	scope := "Application"
	if filepath.Base(dir) == "infra-index" {
		scope = "Shared index"
	}
	for _, a := range args {
		if a == "--target" {
			return scope + " prerequisites"
		}
	}
	return scope + " full stack"
}
func (w *bootstrapWizard) recordPreview(scope string, changes map[string]int, successful bool) {
	if w.memory.Summary.Previews == nil {
		w.memory.Summary.Previews = map[string]previewSummary{}
	}
	w.memory.Summary.Previews[scope] = previewSummary{time.Now().UTC().Format(time.RFC3339), changes, successful}
	if err := w.saveMemory(); err != nil {
		fmt.Println("Could not save preview counts; this summary is available only for the current run.")
	}
	w.showSetupSummary()
}
func (w *bootstrapWizard) summaryStep(key string) error {
	if w.memory.Summary.Steps == nil {
		w.memory.Summary.Steps = map[string]bool{}
	}
	w.memory.Summary.Steps[key] = true
	return w.saveMemory()
}
func (w *bootstrapWizard) setupSummary() string {
	first := func(values ...string) string {
		for _, v := range values {
			if v != "" {
				return v
			}
		}
		return "not available yet"
	}
	var b strings.Builder
	fmt.Fprintln(&b, "Attestra • Setup summary")
	for _, row := range [][2]string{
		{"Environment", first(w.environment, w.memory.Environment)},
		{"AWS account", first(w.account, w.memory.Account)},
		{"Region", first(w.o.Region, w.memory.Region)},
		{"State backend", first(w.o.Backend, w.memory.Backend)},
	} {
		fmt.Fprintf(&b, "%-15s %s\n", row[0]+":", row[1])
	}
	dns := map[string]string{"cloudflare": "Cloudflare", "route53": "Amazon Route 53"}[w.memory.Settings["dnsProvider"]]
	fmt.Fprintf(&b, "%-15s %s\n", "DNS provider:", first(dns))
	for _, key := range []string{"App", "API", "Sender domain", "Sender email", "Index"} {
		value := w.memory.Summary.Domains[key]
		if value == "" {
			switch key {
			case "App":
				value = w.memory.Settings["origin"]
			case "Sender domain":
				value = w.memory.Settings["senderDomain"]
			case "Sender email":
				value = w.memory.Settings["sender"]
			}
		}
		fmt.Fprintf(&b, "%-15s %s\n", key+":", first(value))
	}
	fmt.Fprintln(&b, "\nEstimated resource changes (last recorded previews; not remaining changes or costs):")
	if len(w.memory.Summary.Previews) == 0 {
		fmt.Fprintln(&b, "  Preview counts not available yet.")
	}
	scopes := []string{}
	for scope := range w.memory.Summary.Previews {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		p := w.memory.Summary.Previews[scope]
		detail := "not available (preview failed or summary missing)"
		if p.Successful && p.Changes != nil {
			detail = formatResourceChanges(p.Changes)
		}
		fmt.Fprintf(&b, "  %s: %s [%s]\n", scope, detail, p.ObservedAt)
	}
	fmt.Fprintln(&b, "  Previews are separate snapshots; counts are not added together.")
	fmt.Fprintln(&b, "\nRecorded setup progress (pending includes steps not yet rechecked):")
	for _, step := range summarySteps {
		status := "PENDING"
		if w.memory.Summary.Steps[step.key] {
			status = "DONE"
		}
		fmt.Fprintf(&b, "  %-8s %s\n", status, step.label)
	}
	fmt.Fprintln(&b, "\nManual follow-up: review/commit configuration; load android-config/<environment>.properties in Android; test email/sign-in and any enabled ID capture. Website hosting is separate.")
	return b.String()
}
func (w *bootstrapWizard) showSetupSummary() {
	if w.environment == "" {
		return
	}
	fmt.Println(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(w.setupSummary()))
}

// Event logs may contain resource details. Keep them in a private temporary
// directory, retain only aggregate counts, and remove them on every exit path.
func previewEventLog() (string, func(), error) {
	dir, err := os.MkdirTemp("", "attestra-preview-")
	if err != nil {
		return "", func() {}, err
	}
	return filepath.Join(dir, "events.json"), func() { os.RemoveAll(dir) }, nil
}

func (w *bootstrapWizard) recordSummaryHealth(passed bool) error {
	if passed {
		return w.summaryStep("health")
	}
	delete(w.memory.Summary.Steps, "health")
	w.memory.Completion = nil
	if w.memory.Stage == "complete" {
		w.memory.Stage = "deployed"
	}
	return w.saveMemory()
}
func (w *bootstrapWizard) checkSummaryHealth() error {
	checkErr := runEnvironmentHealth(w.r, w.o)
	if err := w.recordSummaryHealth(checkErr == nil); err != nil {
		return err
	}
	return checkErr
}
