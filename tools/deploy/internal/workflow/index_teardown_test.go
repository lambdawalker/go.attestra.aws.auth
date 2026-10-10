package workflow

import (
	"encoding/json"
	"errors"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexTeardownRequiresRetiredUnlockedRows(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		allowed bool
	}{
		{`{"Items":[]}`, true},
		{`{"Items":[{"id":{"S":"env#dev"},"deleted":{"BOOL":true}}]}`, true},
		{`{"Items":[{"id":{"S":"env#dev"},"deleted":{"BOOL":false}}]}`, false},
		{`{"Items":[{"id":{"S":"env#dev"},"deleted":{"BOOL":true},"lock":{"S":"active"}}]}`, false},
		{`{"Items":[{"id":{"S":"env#dev"},"deleted":{"BOOL":true},"entry":{"M":{}}}]}`, false},
		{`{"Items":[{}]}`, false}, {`{}`, false}, {`{"Items":null}`, false},
	} {
		if err := indexRegistryEmpty([]byte(tc.raw)); (err == nil) != tc.allowed {
			t.Fatalf("%s: %v", tc.raw, err)
		}
	}
}
func TestIndexTeardownRejectsWrongStackAndProtection(t *testing.T) {
	for _, tc := range []struct {
		name, kind, project string
		protected, allowed  bool
	}{
		{"environment-registry", "aws:dynamodb/table:Table", "attestra-index", true, true},
		{"other", "aws:dynamodb/table:Table", "attestra-index", true, false},
		{"environment-registry", "aws:dynamodb/table:Table", "attestra-auth-email", true, false},
	} {
		data, _ := json.Marshal(map[string]any{"deployment": map[string]any{"resources": []any{map[string]any{"urn": "urn:pulumi:shared::" + tc.project + "::" + tc.kind + "::" + tc.name, "type": tc.kind, "id": "registry-table", "protect": tc.protected, "outputs": map[string]any{}}}}})
		p := teardownProgress{Values: map[string]string{}}
		if err := captureIndexTeardown(data, &p); (err == nil) != tc.allowed {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestIndexStackListUsesIndexProject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		found bool
	}{{"shared", true}, {"org/attestra-index/shared", true}, {"org/attestra-auth-email/shared", false}} {
		data, _ := json.Marshal([]map[string]string{{"Name": tc.name}})
		found, err := stackListedInProject(data, "shared", "attestra-index")
		if err != nil || found != tc.found {
			t.Fatalf("%s: %t %v", tc.name, found, err)
		}
	}
}

type indexScanAPI struct {
	pages []string
	err   error
	calls int
}

func (a *indexScanAPI) Call(out any, args ...string) (bool, error) {
	a.calls++
	if a.err != nil {
		return false, a.err
	}
	if len(a.pages) == 0 {
		return true, nil
	}
	page := a.pages[0]
	a.pages = a.pages[1:]
	return true, json.Unmarshal([]byte(page), out)
}
func TestIndexTeardownScansEveryPageAndFailsClosed(t *testing.T) {
	a := &indexScanAPI{pages: []string{`{"Items":[],"LastEvaluatedKey":{"id":{"S":"first"}}}`, `{"Items":[{"id":{"S":"qa"},"deleted":{"BOOL":false}}]}`}}
	if err := checkIndexRegistry(a, "registry", false); err == nil || a.calls != 2 {
		t.Fatalf("calls=%d err=%v", a.calls, err)
	}
	a = &indexScanAPI{err: &awsenv.Error{Code: "AccessDeniedException"}}
	if checkIndexRegistry(a, "registry", true) == nil {
		t.Fatal("access denial treated as absence")
	}
	a.err = &awsenv.Error{Code: "ResourceNotFoundException"}
	if checkIndexRegistry(a, "registry", false) == nil {
		t.Fatal("missing table accepted before destroy")
	}
	if err := checkIndexRegistry(a, "registry", true); err != nil {
		t.Fatal(err)
	}
}

type indexDestroyRunner struct {
	calls       []string
	failPreview bool
}

func (r *indexDestroyRunner) Exec(_ string, _ bool, _ string, args ...string) ([]byte, error) {
	command := strings.Join(args, " ")
	r.calls = append(r.calls, command)
	if args[0] == "destroy" && strings.Contains(command, "--preview-only") && r.failPreview {
		return nil, errors.New("preview failed")
	}
	if args[0] == "stack" && args[1] == "export" {
		if len(r.calls) == 1 {
			return []byte(`{"deployment":{"resources":[{"urn":"urn:pulumi:shared::attestra-index::aws:dynamodb/table:Table::environment-registry","protect":true}]}}`), nil
		}
		return []byte(`{"deployment":{"resources":[]}}`), nil
	}
	return nil, nil
}
func TestIndexTeardownStopsOnActiveEnvironmentBeforeUnprotect(t *testing.T) {
	a := &indexScanAPI{pages: []string{`{"Items":[{"id":{"S":"dev"},"deleted":{"BOOL":false}}]}`}}
	r := &indexDestroyRunner{}
	p := teardownProgress{Bucket: "registry"}
	if err := destroyIndexStack(r, a, "infra-index", &p, func() error { return nil }); err == nil || len(r.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
}
func TestIndexTeardownPreviewFailureStopsDeletion(t *testing.T) {
	a := &indexScanAPI{pages: []string{`{"Items":[]}`}}
	r := &indexDestroyRunner{failPreview: true}
	p := teardownProgress{Bucket: "registry", Values: map[string]string{"tableURN": "urn:pulumi:shared::attestra-index::aws:dynamodb/table:Table::environment-registry"}}
	if err := destroyIndexStack(r, a, "infra-index", &p, func() error { return nil }); err == nil {
		t.Fatal("preview failure ignored")
	}
	for _, cmd := range r.calls {
		if strings.HasPrefix(cmd, "destroy ") && !strings.Contains(cmd, "--preview-only") {
			t.Fatal("destroyed after failed preview")
		}
		if strings.Contains(cmd, "--all") {
			t.Fatal("unprotected unrelated resources")
		}
	}
	if p.Destroyed {
		t.Fatal("recorded false success")
	}
}
func TestIndexTeardownCompletesAndResumesWithoutDestroyingAgain(t *testing.T) {
	a := &indexScanAPI{pages: []string{`{"Items":[]}`}}
	r := &indexDestroyRunner{}
	p := teardownProgress{Bucket: "registry", Values: map[string]string{"tableURN": "urn:pulumi:shared::attestra-index::aws:dynamodb/table:Table::environment-registry"}}
	if err := destroyIndexStack(r, a, "infra-index", &p, func() error { return nil }); err != nil || !p.Destroyed {
		t.Fatal(err, p.Destroyed)
	}
	count := len(r.calls)
	if err := destroyIndexStack(r, a, "infra-index", &p, func() error { return nil }); err != nil || len(r.calls) != count {
		t.Fatal("repeated destruction", err)
	}
}
func TestIndexTeardownPendingBlocksSetupAndDenialIsNotAbsence(t *testing.T) {
	if checkIndexTeardownPending(&indexScanAPI{}, "bucket", "account") == nil {
		t.Fatal("accepted pending teardown")
	}
	for _, tc := range []struct {
		code    string
		allowed bool
	}{{"404", true}, {"NoSuchKey", true}, {"AccessDenied", false}} {
		err := checkIndexTeardownPending(&indexScanAPI{err: &awsenv.Error{Code: tc.code}}, "bucket", "account")
		if (err == nil) != tc.allowed {
			t.Fatal(tc, err)
		}
	}
}
func TestIndexTeardownArchivesCompletedLifecycle(t *testing.T) {
	root := stateTestDir(t)
	p := teardownProgress{Complete: true, Values: map[string]string{"account": "123456789012", "region": "us-east-2"}}
	path := filepath.Join(root, ".attestra", "teardown-index.123456789012.us-east-2.local.json")
	if err := p.save(path); err != nil {
		t.Fatal(err)
	}
	if err := archiveCompletedIndexTeardown(root, "123456789012", "us-east-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("old completed receipt still blocks new lifecycle")
	}
	p.Complete = false
	if err := p.save(path); err != nil {
		t.Fatal(err)
	}
	if archiveCompletedIndexTeardown(root, "123456789012", "us-east-2") == nil {
		t.Fatal("archived unfinished teardown")
	}
}
