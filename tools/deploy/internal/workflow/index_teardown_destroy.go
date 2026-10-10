package workflow

import (
	"encoding/json"
	"errors"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
)

func destroyIndexStack(r commandRunner, a awsenv.API, dir string, p *teardownProgress, persist func() error) error {
	var err error
	run := func(capture bool, args ...string) ([]byte, error) { return r.Exec(dir, capture, "pulumi", args...) }
	if !p.Destroyed {
		if err = checkIndexRegistry(a, p.Bucket, p.IndexRetired); err != nil {
			return err
		}
		p.IndexRetired = true
		if err = persist(); err != nil {
			return err
		}
		if urn := p.Values["tableURN"]; urn != "" {
			data, e := run(true, "stack", "export", "--stack", "shared")
			if e != nil {
				return e
			}
			// A previous destroy may already have removed the table from state.
			var current struct {
				Deployment struct {
					Resources []struct {
						URN     string
						Protect bool
					}
				}
			}
			if json.Unmarshal(data, &current) != nil {
				return errors.New("invalid current index state")
			}
			for _, resource := range current.Deployment.Resources {
				if resource.URN == urn && resource.Protect {
					if _, err = run(false, "state", "unprotect", urn, "--stack", "shared", "--yes", "--non-interactive"); err != nil {
						return err
					}
				}
			}
		}
		if _, err = run(false, "destroy", "--stack", "shared", "--preview-only", "--non-interactive"); err != nil {
			return err
		}
		if _, err = run(false, "destroy", "--stack", "shared", "--yes", "--non-interactive"); err != nil {
			return err
		}
		data, e := run(true, "stack", "export", "--stack", "shared")
		if e != nil {
			return e
		}
		if err = ensureDestroyed(data); err != nil {
			return err
		}
		p.Destroyed = true
		if err = persist(); err != nil {
			return err
		}
	}
	return nil
}
