package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	envregistry "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/registry"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"
)

type teardownResult struct{ Name, Status, Detail string }
type teardownReport []teardownResult

func (r teardownReport) Err() error {
	for _, v := range r {
		if v.Status == "FAILED" || v.Status == "REMAINS" {
			return errors.New("teardown validation failed; inspect the report, resolve remaining resources or access errors, then rerun teardown with its saved checkpoint")
		}
	}
	return nil
}
func (r teardownReport) Print() {
	fmt.Println("\nFinal teardown validation")
	for _, v := range r {
		fmt.Printf("%s • %s: %s\n", v.Status, v.Name, v.Detail)
	}
}

func verifyTeardownIndex(c *envregistry.Client) error {
	req, err := http.NewRequest("GET", c.URL+"/v1/environments", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("cannot read environment index")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("index read returned HTTP %d", response.StatusCode)
	}
	// A malformed/error response must not be interpreted as an empty index.
	var body struct {
		SchemaVersion int
		Environments  *[]struct{ ID string }
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&body); err != nil || body.SchemaVersion != 1 || body.Environments == nil {
		return errors.New("invalid environment index response")
	}
	for _, entry := range *body.Environments {
		if entry.ID == "" {
			return errors.New("invalid index entry")
		}
		if entry.ID == c.Environment {
			return errTeardownResourceRemains
		}
	}
	return nil
}

func verifyTeardown(root string, p *teardownProgress, a awsenv.API, r commandRunner, g *github.Client, cf *dns.Client, index *envregistry.Client) teardownReport {
	report := teardownReport{}
	check := func(name string, err error) {
		status, detail := "DELETED", "absence verified"
		if err != nil {
			status, detail = "FAILED", err.Error()
			if errors.Is(err, errTeardownResourceRemains) {
				status = "REMAINS"
			}
		}
		report = append(report, teardownResult{name, status, detail})
	}
	if !p.InventoryCaptured {
		check("AWS inventory", errors.New("legacy checkpoint has no pre-destroy inventory; restore evidence from a pre-destroy stack export for manual verification; cannot certify all resources"))
	}
	cache := map[string]error{}
	for _, probe := range p.Inventory {
		name := probe.Name
		if i := strings.LastIndex(name, "::"); i >= 0 {
			name = name[i+2:]
		}
		if probe.Retained {
			report = append(report, teardownResult{name, "RETAINED", "external resource or Pulumi retainOnDelete"})
			continue
		}
		key := probe.Kind + "\x00" + probe.ID + "\x00" + probe.Zone + "\x00" + probe.RecordType
		err, ok := cache[key]
		if !ok {
			err = verifyTeardownProbe(a, probe)
			cache[key] = err
		}
		check(name, err)
	}
	role := strings.Split(p.Values["AWS_ROLE_ARN"], "/")
	check("Deployment role", verifyTeardownProbe(a, teardownProbe{Kind: "role", ID: role[len(role)-1]}))
	data, err := r.Exec(filepath.Join(root, "infra"), true, "pulumi", "stack", "ls", "--json", "--non-interactive")
	if err == nil {
		var found bool
		found, err = stackListed(data, p.Environment)
		if err == nil && found {
			err = errTeardownResourceRemains
		}
	}
	check("Pulumi stack", err)
	found, err := g.Request("GET", github.EnvironmentPath(p.Repository, p.Environment), nil, nil)
	if err == nil && found {
		err = errTeardownResourceRemains
	}
	check("GitHub environment", err)
	if p.DNSSkipped {
		report = append(report, teardownResult{"Cloudflare DNS", "SKIPPED", "records retained by your choice; manual cleanup required"})
	} else if len(p.Desired) > 0 && p.Zone != "" {
		if cf == nil {
			check("Cloudflare DNS", errors.New("Cloudflare credentials unavailable for final read-back"))
		} else {
			for _, want := range p.Desired {
				records, e := cf.Records(p.Zone, want.Name)
				if e == nil {
					var matches []dns.Record
					matches, e = dns.DeletionCandidates(want, records)
					if e == nil && len(matches) > 0 {
						e = errTeardownResourceRemains
					}
				}
				check("DNS "+want.Type+" "+want.Name, e)
			}
		}
	} else if len(p.Desired) > 0 && !p.DNSDone {
		check("Cloudflare DNS", errors.New("no verified DNS cleanup or zone evidence"))
	}
	paths := []string{filepath.Join("infra", "Pulumi."+p.Environment+".yaml"), filepath.Join(".attestra", "bootstrap."+p.Environment+".local.json"), filepath.Join(".attestra", "bootstrap."+p.Environment+".local.json.bak"), filepath.Join("android-config", p.Environment+".properties")}
	if p.Environment == "dev" {
		paths = append(paths, filepath.Join(".attestra", "bootstrap.local.json"), filepath.Join(".attestra", "bootstrap.local.json.bak"))
	}
	for _, path := range paths {
		_, e := os.Lstat(filepath.Join(root, path))
		if os.IsNotExist(e) {
			e = nil
		} else if e == nil {
			e = errTeardownResourceRemains
		}
		check(path, e)
	}
	if p.DeleteVault {
		path, _, e := credentialvault.Location(p.Repository, p.Environment)
		if e == nil {
			_, e = os.Lstat(path)
			if os.IsNotExist(e) {
				e = nil
			} else if e == nil {
				e = errTeardownResourceRemains
			}
		}
		check("Credential vault", e)
	} else {
		report = append(report, teardownResult{"Credential vault", "RETAINED", "deletion not requested (if present)"})
	}
	if index != nil {
		check("Public environment index entry", verifyTeardownIndex(index))
	} else {
		report = append(report, teardownResult{"Environment index", "SKIPPED", "no index was configured for this environment"})
	}
	report = append(report, teardownResult{"Shared infrastructure and recovery data", "RETAINED", "state bucket/history, shared index/OIDC/DNS zone, teardown checkpoint and YAML backup"}, teardownResult{"Service-created history outside Pulumi", "RETAINED", "Lambda log groups and AWS-managed backups may remain; not part of the captured stack"})
	return report
}

// Retry the full read-only pass only for resources still visible after deletion.
// Credential/permission/malformed-response failures are reported immediately.
func finalizeTeardownValidation(p *teardownProgress, persist func() error, verify func() teardownReport, pause func(time.Duration)) error {
	for attempt := 0; attempt < 3; attempt++ {
		p.Validation = verify()
		p.ValidatedAt = time.Now().UTC().Format(time.RFC3339)
		p.Validation.Print()
		if e := persist(); e != nil {
			return e
		}
		remaining, failed := false, false
		for _, v := range p.Validation {
			remaining = remaining || v.Status == "REMAINS"
			failed = failed || v.Status == "FAILED"
		}
		if !remaining || failed || attempt == 2 {
			return p.Validation.Err()
		}
		fmt.Println("Waiting 5 seconds for deletion to become visible…")
		pause(5 * time.Second)
	}
	return p.Validation.Err()
}
