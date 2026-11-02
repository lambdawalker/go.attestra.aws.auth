package workflow

import (
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNukeMissingWorkflowDoesNotBlockButOlderActiveRunsDo(t *testing.T) {
	for _, tc := range []struct {
		body    string
		blocked bool
	}{{`{"workflow_runs":[]}`, false}, {`{"workflow_runs":[{"path":".github/workflows/verify.yml"}]}`, false}, {`{"workflow_runs":[{"path":".github/workflows/deploy.yml"}]}`, true}, {`{}`, true}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/o/r/actions/workflows/deploy.yml" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.body))
		}))
		g := github.NewClient("test")
		g.BaseURL = s.URL
		err := ensureNoNukeDeployments(g, "o/r")
		s.Close()
		if (err != nil) != tc.blocked {
			t.Fatalf("body=%s err=%v", tc.body, err)
		}
	}
}
