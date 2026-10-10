package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type verificationAWS struct {
	found    bool
	err      error
	response string
	calls    int
	args     []string
}

func (a *verificationAWS) call(v any, args ...string) (bool, error) {
	a.calls++
	a.args = append([]string(nil), args...)
	if v != nil && a.response != "" {
		if e := json.Unmarshal([]byte(a.response), v); e != nil {
			return false, e
		}
	}
	return a.found, a.err
}

func TestTeardownCapturesInventoryWithoutSecrets(t *testing.T) {
	p := teardownProgress{}
	state := []byte(`{"deployment":{"resources":[{"type":"pulumi:pulumi:Stack"},{"type":"aws:lambda/function:Function","urn":"function","id":"qa-worker","outputs":{"environment":{"variables":{"SECRET":"never-save-this"}}}},{"type":"aws:lambda/permission:Permission","urn":"permission","id":"allow","outputs":{"function":"qa-worker"}}]}}`)
	if e := captureTeardownEvidence(state, &p); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p)
	if !p.InventoryCaptured || len(p.Inventory) != 2 || strings.Contains(string(b), "never-save-this") {
		t.Fatalf("inventory missing or unsafe: %s", b)
	}
}
func TestTeardownVerificationRejectsPresentAndDeniedResources(t *testing.T) {
	probe := teardownProbe{Name: "worker", Kind: "lambda", ID: "qa-worker"}
	for _, tc := range []struct {
		name  string
		found bool
		err   error
		ok    bool
	}{
		{"present", true, nil, false}, {"absent", false, &awsSetupError{Code: "ResourceNotFoundException"}, true}, {"denied", false, &awsSetupError{Code: "AccessDeniedException"}, false}, {"network", false, errors.New("network"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &verificationAWS{found: tc.found, err: tc.err}
			e := verifyTeardownProbe(a, probe)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
}
func TestTeardownVerificationRejectsUnknownAndMalformedEvidence(t *testing.T) {
	for _, p := range []teardownProbe{{Kind: "unknown", ID: "x"}, {Kind: "lambda"}} {
		a := &verificationAWS{}
		if verifyTeardownProbe(a, p) == nil || a.calls != 0 {
			t.Fatal("unsupported evidence accepted")
		}
	}
}
func TestTeardownListVerificationNeedsExplicitEmptyResult(t *testing.T) {
	for _, raw := range []string{`{}`, `{"VerificationAttributes":{"qa.example.com":{}}}`} {
		if verifyTeardownProbe(&verificationAWS{found: true, response: raw}, teardownProbe{Kind: "ses", ID: "qa.example.com"}) == nil {
			t.Fatal("accepted", raw)
		}
	}
	if e := verifyTeardownProbe(&verificationAWS{found: true, response: `{"VerificationAttributes":{}}`}, teardownProbe{Kind: "ses", ID: "qa.example.com"}); e != nil {
		t.Fatal(e)
	}
}
func TestTeardownFinalReportFailsClosed(t *testing.T) {
	for _, status := range []string{"REMAINS", "FAILED"} {
		if (teardownReport{{Status: status}}).Err() == nil {
			t.Fatal(status)
		}
	}
	if (teardownReport{{Status: "DELETED"}, {Status: "RETAINED"}, {Status: "SKIPPED"}}).Err() != nil {
		t.Fatal("approved exceptions should be reported separately")
	}
}

func TestTeardownRoute53UsesAPIInputForPagination(t *testing.T) {
	a := &verificationAWS{found: true, response: `{"ResourceRecordSets":[]}`}
	if e := verifyTeardownProbe(a, teardownProbe{Kind: "record", ID: "qa.example.com", Zone: "Z123", RecordType: "CNAME"}); e != nil {
		t.Fatal(e)
	}
	found := false
	for i, arg := range a.args {
		if arg == "--cli-input-json" {
			var input map[string]string
			if json.Unmarshal([]byte(a.args[i+1]), &input) != nil || input["StartRecordName"] != "qa.example.com" || input["MaxItems"] != "1" {
				t.Fatal(a.args)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("Route53 paginator hides start-record flags; use API JSON input", a.args)
	}
}
func TestTeardownIndexReadback(t *testing.T) {
	for _, body := range []string{`{"schemaVersion":1,"environments":[]}`, `{}`, `{"schemaVersion":1,"environments":[{"id":"qa"}]}`, `{"schemaVersion":1,"environments":null}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		e := verifyTeardownIndex(&indexClient{URL: server.URL, Environment: "qa", HTTP: server.Client()})
		server.Close()
		if (e == nil) != (body == `{"schemaVersion":1,"environments":[]}`) {
			t.Fatal(body, e)
		}
	}
}

type teardownVerifyRunner struct {
	body string
	err  error
}

func (r teardownVerifyRunner) Exec(string, bool, string, ...string) ([]byte, error) {
	return []byte(r.body), r.err
}
func TestTeardownValidationChecksLocalFilesAndLegacyInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer server.Close()
	g := newGitHubClient("test")
	g.base = server.URL
	root := t.TempDir()
	p := teardownProgress{Environment: "qa", Repository: "owner/repo", Values: map[string]string{"AWS_ROLE_ARN": "arn:aws:iam::123:role/qa"}}
	verify := func() teardownReport {
		return verifyTeardown(root, &p, &verificationAWS{}, teardownVerifyRunner{body: "[]"}, g, nil, nil)
	}
	if verify().Err() == nil {
		t.Fatal("legacy missing inventory passed")
	}
	p.InventoryCaptured = true
	if e := verify().Err(); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(root, "android-config"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "android-config", "qa.properties"), []byte("api"), 0600); e != nil {
		t.Fatal(e)
	}
	if verify().Err() == nil {
		t.Fatal("remaining Android export passed")
	}
	os.Remove(filepath.Join(root, "android-config", "qa.properties"))
	os.MkdirAll(filepath.Join(root, "infra"), 0700)
	os.WriteFile(filepath.Join(root, "infra", "Pulumi.qa.yaml"), []byte("config: {}"), 0600)
	if verify().Err() == nil {
		t.Fatal("remaining stack YAML passed")
	}

}
func TestTeardownValidationRetriesOnlyRemainingAndPersistsFailure(t *testing.T) {
	for _, status := range []string{"REMAINS", "FAILED", "DELETED"} {
		p := teardownProgress{}
		calls, saves, waits := 0, 0, 0
		err := finalizeTeardownValidation(&p, func() error { saves++; return nil }, func() teardownReport { calls++; return teardownReport{{Name: "resource", Status: status}} }, func(time.Duration) { waits++ })
		expected := 1
		if status == "REMAINS" {
			expected = 3
		}
		if calls != expected || saves != expected || waits != expected-1 || p.ValidatedAt == "" || p.Complete || (err == nil) != (status == "DELETED") {
			t.Fatal(status, calls, saves, waits, err)
		}
	}
}

func TestTeardownInventoryMergeDoesNotCertifyLegacyCheckpoint(t *testing.T) {
	p := teardownProgress{}
	if e := captureTeardownInventory([]byte(`{"deployment":{"resources":[]}}`), &p); e != nil {
		t.Fatal(e)
	}
	if p.InventoryCaptured {
		t.Fatal("remaining export cannot certify original legacy inventory")
	}
}
func TestTeardownInventoryRequiresResourceArray(t *testing.T) {
	for _, raw := range []string{`{"deployment":{}}`, `{"deployment":{"resources":null}}`} {
		if captureTeardownInventory([]byte(raw), &teardownProgress{}) == nil {
			t.Fatal("accepted malformed export", raw)
		}
	}
}
func TestTeardownRoute53RejectsMalformedRecords(t *testing.T) {
	if verifyTeardownProbe(&verificationAWS{found: true, response: `{"ResourceRecordSets":[{}]}`}, teardownProbe{Kind: "record", ID: "qa.example.com", Zone: "Z123", RecordType: "CNAME"}) == nil {
		t.Fatal("malformed record accepted")
	}
}
