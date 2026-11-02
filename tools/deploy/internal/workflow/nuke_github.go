package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
)

// Inspect repository runs rather than requiring deploy.yml to still exist.
// A removed workflow is a missing artifact, not a teardown failure; its older
// queued/running jobs must still prevent racing the teardown.
func ensureNoNukeDeployments(g *github.Client, repo string) error {
	for _, status := range []string{"queued", "in_progress", "waiting", "pending", "requested"} {
		for page := 1; ; page++ {
			var result struct {
				WorkflowRuns *[]struct{ Path string } `json:"workflow_runs"`
			}
			found, err := g.Request("GET", fmt.Sprintf("/repos/%s/actions/runs?per_page=100&page=%d&status=%s", repo, page, status), nil, &result)
			if err != nil {
				return err
			}
			if !found || result.WorkflowRuns == nil {
				return errors.New("cannot establish whether deployments are active")
			}
			for _, run := range *result.WorkflowRuns {
				if run.Path == "" {
					return errors.New("workflow run missing its path")
				}
				path := strings.SplitN(run.Path, "@", 2)[0]
				if path == ".github/workflows/deploy.yml" || path == ".github/workflows/deploy.yaml" {
					return errors.New("deployment workflow has active or waiting runs; finish/cancel them before nuke teardown")
				}
			}
			if len(*result.WorkflowRuns) < 100 {
				break
			}
		}
	}
	return nil
}
