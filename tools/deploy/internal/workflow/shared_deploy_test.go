package workflow

import (
	"encoding/json"
	"errors"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"strings"
	"testing"
)

func TestSharedScopeFlags(t *testing.T) {
	for _, name := range []string{"stack", "backend", "ci", "bootstrap", "build", "publish-index", "release-index-lock", "teardown", "health-check"} {
		if validateDeployScope("shared", []string{name}) == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if validateDeployScope("shared", []string{"scope", "repo-root", "region", "sso", "profile"}) != nil {
		t.Fatal("rejected shared arguments")
	}
	if validateDeployScope("app", nil) != nil || validateDeployScope("admin", nil) == nil {
		t.Fatal("invalid scopes")
	}
	if got := normalizeArguments([]string{"-Scope", "shared"}); got[0] != "-scope" {
		t.Fatal(got)
	}
}

type sharedRunner struct {
	calls   []string
	fail    int
	config  string
	outputs string
}

func (r *sharedRunner) Exec(dir string, capture bool, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if len(r.calls) == r.fail {
		return nil, errors.New("failed")
	}
	if name == "pulumi" && !strings.HasSuffix(dir, "infra-index") {
		return nil, errors.New("environment backend touched")
	}
	if strings.Contains(call, "config --json") {
		return []byte(r.config), nil
	}
	if strings.Contains(call, "stack export") {
		return []byte(`{"deployment":{"secrets_providers":{"type":"passphrase"}}}`), nil
	}
	if strings.Contains(call, "stack output") {
		return []byte(r.outputs), nil
	}
	return nil, nil
}
func sharedTestRunner() *sharedRunner {
	return &sharedRunner{config: `{"aws:region":{"value":"us-east-2"},"attestra-index:domain":{"value":"index.example.com"},"attestra-index:adminDomain":{"value":"admin.example.com"}}`, outputs: `{"adminPoolId":"pool","adminLoginDomain":"https://login.example.com","runtimeTableName":"runtime","apiUrl":"https://api.example.com"}`}
}
func TestSharedDeploymentIsolationAndFailureStops(t *testing.T) {
	r := sharedTestRunner()
	builds := 0
	build := func(string) error { builds++; return nil }
	if err := deploySharedStack(r, options{Root: t.TempDir(), Stack: "shared", Region: "us-east-2"}, build); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatal(builds)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "--yes") || strings.Contains(call, "stack init") || strings.Contains(call, "-environment") {
			t.Fatal(call)
		}
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "-auth-only") {
		t.Fatal(r.calls)
	}
	count := len(r.calls)
	for i := 1; i <= count; i++ {
		r := sharedTestRunner()
		r.fail = i
		if deploySharedStack(r, options{Root: t.TempDir(), Stack: "shared", Region: "us-east-2"}, build) == nil || len(r.calls) != i {
			t.Fatalf("continued after failure %d: %v", i, r.calls)
		}
	}
}
func TestSharedDeploymentRejectsIncompleteConfigBeforeBuild(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"aws:region":{"value":"us-west-2"}}`, `{"aws:region":{"value":"us-east-2"},"aws:profile":{"value":"other"}}`} {
		r := sharedTestRunner()
		r.config = cfg
		err := deploySharedStack(r, options{Root: t.TempDir(), Stack: "shared", Region: "us-east-2"}, func(string) error { t.Fatal("built invalid stack"); return nil })
		if err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

type sharedLockAPI struct {
	calls                                          []string
	failAcquire, missingETag, pending, failRelease bool
}

func (a *sharedLockAPI) Call(out any, args ...string) (bool, error) {
	call := strings.Join(args, " ")
	a.calls = append(a.calls, call)
	switch args[1] {
	case "put-object":
		if a.failAcquire {
			return false, errors.New("busy")
		}
		if !a.missingETag {
			b := []byte(`{"ETag":"lease-etag"}`)
			if e := json.Unmarshal(b, out); e != nil {
				return false, e
			}
		}
	case "head-object":
		if !a.pending {
			return false, &awsenv.Error{Code: "404"}
		}
	case "delete-object":
		if a.failRelease {
			return false, errors.New("delete failed")
		}
	}
	return true, nil
}
func TestSharedLockOwnershipAndFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		api                   sharedLockAPI
		workFails             bool
		wantWork, wantRelease bool
	}{
		{name: "success", wantWork: true, wantRelease: true},
		{name: "work failure", workFails: true, wantWork: true, wantRelease: true},
		{name: "locked", api: sharedLockAPI{failAcquire: true}},
		{name: "missing etag", api: sharedLockAPI{missingETag: true}},
		{name: "teardown", api: sharedLockAPI{pending: true}, wantRelease: true},
		{name: "release failure", api: sharedLockAPI{failRelease: true}, wantWork: true, wantRelease: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worked := false
			err := withSharedDeployLock(&tc.api, options{Backend: "s3://shared-state"}, "123456789012", t.TempDir(), func() error {
				worked = true
				if tc.workFails {
					return errors.New("work failed")
				}
				return nil
			})
			if worked != tc.wantWork {
				t.Fatal("unexpected work execution")
			}
			released := false
			for _, call := range tc.api.calls {
				if strings.Contains(call, "delete-object") {
					released = true
					if !strings.Contains(call, "--if-match lease-etag") {
						t.Fatal("unconditional unlock")
					}
				}
			}
			if released != tc.wantRelease {
				t.Fatal("unexpected lock release")
			}
			if (err == nil) != (tc.name == "success") {
				t.Fatal(err)
			}
			if !strings.Contains(tc.api.calls[0], "--if-none-match *") || !strings.Contains(tc.api.calls[0], "bootstrap.lock") {
				t.Fatal("wrong lock")
			}
		})
	}
}
func TestSharedBuildFailureStopsBeforePreview(t *testing.T) {
	r := sharedTestRunner()
	if deploySharedStack(r, options{Root: t.TempDir(), Region: "us-east-2"}, func(string) error { return errors.New("build failed") }) == nil {
		t.Fatal("ignored build failure")
	}
	for _, c := range r.calls {
		if strings.Contains(c, "preview") || strings.Contains(c, " up ") {
			t.Fatal("deployed after failed build")
		}
	}
}
