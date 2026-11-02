package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func nukeProbeLabel(t *nukeTarget, p teardownProbe) string {
	return t.Project + "/" + t.Stack + " • " + t.Backend + " • " + p.Name + " [" + p.Kind + " " + p.ID + "]"
}

// The original teardown results remain intact. CheckResults are fresh evidence,
// not deletion authorization: this path never calls cleanup or recovery methods.
func (n *nukeWorkflow) checkLastNuke() error {
	if len(n.receipt.Results) == 0 {
		return errors.New("previous nuke report has no results to check")
	}
	n.receipt.CheckResults = nil
	n.receipt.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	// Deduplicate repeated operations, keeping the latest status for context.
	latest := map[string]nukeResult{}
	var names []string
	for _, r := range n.receipt.Results {
		if _, ok := latest[r.Name]; !ok {
			names = append(names, r.Name)
		}
		latest[r.Name] = r
	}
	counts := map[string]int{}
	for _, name := range names {
		previous := latest[name]
		if previous.Status == "INFO" {
			continue
		}
		err := n.checkNukeArtifact(name)
		r := nukeResult{Name: name, Status: "ABSENT", Detail: "absence confirmed by post-nuke check"}
		if errors.Is(err, errTeardownResourceRemains) {
			r.Status, r.Detail = "REMAINS", err.Error()
		} else if err != nil {
			r.Status, r.Detail = "UNVERIFIED", err.Error()
		}
		r.Detail = "Previous " + previous.Status + "; " + r.Detail
		n.receipt.CheckResults = append(n.receipt.CheckResults, r)
		counts[r.Status]++
		fmt.Printf("%s • %s: %s\n", r.Status, r.Name, r.Detail)
	}
	if err := n.save(); err != nil {
		return fmt.Errorf("save post-nuke check: %w", err)
	}
	fmt.Printf("\nPost-nuke check: ABSENT %d • REMAINS %d • UNVERIFIED %d\nReport: %s\n", counts["ABSENT"], counts["REMAINS"], counts["UNVERIFIED"], n.path)
	if len(n.receipt.CheckResults) == 0 || counts["REMAINS"]+counts["UNVERIFIED"] > 0 {
		return errors.New("post-nuke check incomplete; inspect CheckResults in the saved report")
	}
	return nil
}

func (n *nukeWorkflow) checkNukeArtifact(name string) error {
	for i := range n.receipt.Targets {
		t := &n.receipt.Targets[i]
		for _, p := range t.Evidence.Inventory {
			if name == nukeProbeLabel(t, p) {
				return verifyTeardownProbe(n.api(n.probeRegion(t, p)), p)
			}
		}
		if name == t.Project+"/"+t.Stack+" • "+t.Backend+" stack" {
			return n.checkNukeStack(*t)
		}
		for _, want := range t.Evidence.Desired {
			if name == "DNS "+want.Type+" "+want.Name {
				return n.checkNukeDNS(want)
			}
		}
	}
	for _, b := range n.receipt.Backends {
		if name == "State bucket "+b.Bucket {
			found, err := n.api(b.Region).Call(nil, "s3api", "head-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
			if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
				return nil
			}
			return nukeCheckPresence(found, err)
		}
	}
	var keyChecks []func() error
	for _, k := range n.receipt.Keys {
		if name == "KMS "+k.ID {
			keyChecks = append(keyChecks, func() error {
				m, found, err := nukeDescribeKey(n.api(k.Region), k.ID)
				if err != nil || !found {
					return err
				}
				return fmt.Errorf("%w in %s: KMS state %s; deletion date %s", errTeardownResourceRemains, k.Region, m.KeyState, nukeDeletionDate(m.DeletionDate))
			})
		}
	}
	if len(keyChecks) > 0 {
		return nukeCheckAll(keyChecks)
	}
	if strings.HasPrefix(name, "Backup ") {
		arn := strings.TrimPrefix(name, "Backup ")
		parts := strings.SplitN(arn, ":", 6)
		if len(parts) != 6 || parts[0] != "arn" || parts[2] != "dynamodb" || parts[3] == "" || parts[4] != n.receipt.Account || !strings.Contains(parts[5], "/backup/") {
			return errors.New("invalid saved backup identity")
		}
		var body struct {
			BackupDescription struct{ BackupDetails struct{ BackupStatus string } }
		}
		found, err := n.api(parts[3]).Call(&body, "dynamodb", "describe-backup", "--backup-arn", arn)
		if nukeMissing(err, "BackupNotFoundException") {
			return nil
		}
		if err != nil {
			return err
		}
		status := body.BackupDescription.BackupDetails.BackupStatus
		if !found || status == "" {
			return errors.New("incomplete backup description")
		}
		if status == "DELETED" {
			return nil
		}
		return fmt.Errorf("%w: backup status %s", errTeardownResourceRemains, status)
	}
	if strings.HasPrefix(name, "Deployment role ") {
		arn := strings.TrimPrefix(name, "Deployment role ")
		prefix := "arn:aws:iam::" + n.receipt.Account + ":role/"
		if !strings.HasPrefix(arn, prefix) || strings.TrimPrefix(arn, prefix) == "" {
			return errors.New("invalid saved deployment role identity")
		}
		role := filepath.Base(strings.TrimPrefix(arn, prefix))
		return verifyTeardownProbe(n.api("us-east-2"), teardownProbe{Kind: "role", ID: role})
	}
	if strings.HasPrefix(name, "GitHub environment ") {
		if n.g == nil {
			return errors.New("GitHub credentials unavailable")
		}
		// GitHub masks inaccessible private resources as 404. Only an
		// accessible, complete environment listing can establish absence.
		for page := 1; ; page++ {
			var body struct{ Environments *[]struct{ Name string } }
			path := fmt.Sprintf("/repos/%s/environments?per_page=100&page=%d", n.receipt.Repository, page)
			found, err := n.g.Request("GET", path, nil, &body)
			if err != nil {
				return err
			}
			if !found || body.Environments == nil {
				return errors.New("GitHub environment listing unavailable; absence cannot be verified")
			}
			for _, e := range *body.Environments {
				if e.Name == "" {
					return errors.New("incomplete GitHub environment identity")
				}
				if e.Name == strings.TrimPrefix(name, "GitHub environment ") {
					return errTeardownResourceRemains
				}
			}
			if len(*body.Environments) < 100 {
				return nil
			}
		}
	}
	if strings.HasPrefix(name, "Log group ") || strings.HasPrefix(name, "Backups for ") {
		var checks []func() error
		seen := map[string]bool{}
		for i := range n.receipt.Targets {
			t := &n.receipt.Targets[i]
			for _, p := range t.Evidence.Inventory {
				region := n.probeRegion(t, p)
				key := region + "/" + p.Kind + "/" + p.ID
				if seen[key] {
					continue
				}
				if p.Kind == "lambda" && name == "Log group /aws/lambda/"+p.ID {
					seen[key] = true
					checks = append(checks, func() error { return n.checkNukeLog(region, "/aws/lambda/"+p.ID) })
				}
				if p.Kind == "table" && name == "Backups for "+p.ID {
					seen[key] = true
					checks = append(checks, func() error { return n.checkNukeBackups(region, p.ID) })
				}
			}
		}
		if len(checks) > 0 {
			return nukeCheckAll(checks)
		}
	}
	for _, prefix := range []string{"Local stack config ", "Local shared stack config "} {
		if strings.HasPrefix(name, prefix) {
			stack := strings.TrimPrefix(name, prefix)
			// Never turn report text into an arbitrary local filesystem path.
			if err := ui.ValidateEnvironment(stack); err != nil {
				return err
			}
			dir := "infra"
			if prefix == "Local shared stack config " {
				dir = "infra-index"
			}
			_, err := os.Lstat(filepath.Join(n.root, dir, "Pulumi."+stack+".yaml"))
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return errTeardownResourceRemains
		}
	}
	return errors.New("saved result has no supported resource identity; manual verification required")
}

// Legacy result labels omit regions. Absence requires every matching saved
// regional identity to be absent, including names reused in different regions.
func nukeCheckAll(checks []func() error) error {
	var remains, unverified error
	for _, check := range checks {
		err := check()
		if errors.Is(err, errTeardownResourceRemains) {
			remains = errors.Join(remains, err)
		} else {
			unverified = errors.Join(unverified, err)
		}
	}
	if unverified != nil {
		return unverified
	}
	return remains
}

func nukeCheckPresence(found bool, err error) error {
	if err != nil {
		return err
	}
	if !found {
		return errors.New("service returned no authoritative result")
	}
	return errTeardownResourceRemains
}

func (n *nukeWorkflow) checkNukeStack(t nukeTarget) error {
	a := n.api(t.Region)
	_, err := a.Call(nil, "s3api", "head-bucket", "--bucket", strings.TrimPrefix(t.Backend, "s3://"), "--expected-bucket-owner", n.receipt.Account)
	if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
		return nil
	}
	if err != nil {
		return err
	}
	var r commandRunner = n.runner(t)
	if n.runnerForTarget != nil {
		r = n.runnerForTarget(t)
	}
	data, err := r.Exec(n.dir(t), true, "pulumi", "stack", "ls", "--json", "--non-interactive")
	if err != nil {
		return err
	}
	found, err := stackListedInProject(data, t.Stack, t.Project)
	if err == nil && found {
		return errTeardownResourceRemains
	}
	return err
}

func (n *nukeWorkflow) checkNukeDNS(want dns.Record) error {
	if n.cf == nil {
		return errors.New("Cloudflare token unavailable")
	}
	zones, err := n.cf.Zones(want.Name)
	if err != nil {
		return err
	}
	for _, zone := range zones {
		if !dns.WithinZone(want.Name, zone.Name) {
			continue
		}
		records, err := n.cf.Records(zone.ID, want.Name)
		if err != nil {
			return err
		}
		matches, err := dns.DeletionCandidates(want, records)
		if err == nil && len(matches) > 0 {
			return errTeardownResourceRemains
		}
		return err
	}
	return errors.New("no accessible DNS zone; cannot verify record absence")
}

func (n *nukeWorkflow) checkNukeLog(region, name string) error {
	var body struct {
		LogGroups *[]struct{ LogGroupName string }
	}
	_, err := n.api(region).Call(&body, "logs", "describe-log-groups", "--log-group-name-prefix", name)
	if err != nil {
		return err
	}
	if body.LogGroups == nil {
		return errors.New("incomplete log group listing")
	}
	for _, g := range *body.LogGroups {
		if g.LogGroupName == name {
			return errTeardownResourceRemains
		}
	}
	return nil
}

func (n *nukeWorkflow) checkNukeBackups(region, table string) error {
	var body struct{ BackupSummaries *[]struct{ BackupArn string } }
	_, err := n.api(region).Call(&body, "dynamodb", "list-backups", "--table-name", table, "--backup-type", "ALL")
	if err != nil {
		return err
	}
	if body.BackupSummaries == nil {
		return errors.New("incomplete backup listing")
	}
	if len(*body.BackupSummaries) > 0 {
		return errTeardownResourceRemains
	}
	return nil
}
