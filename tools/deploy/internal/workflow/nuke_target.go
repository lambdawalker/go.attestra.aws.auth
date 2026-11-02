package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func (n *nukeWorkflow) destroyTarget(t *nukeTarget) {
	name := t.Project + "/" + t.Stack + " • " + t.Backend
	if t.Stack == "" || t.Backend == "" || t.Region == "" {
		n.failure(name, errors.New("incomplete target identity"))
		return
	}
	if err := ui.ValidateEnvironment(t.Stack); err != nil {
		n.failure(name, err)
		return
	}
	a := n.api(t.Region)
	before := map[string]bool{}
	defer n.finishTarget(t, before)
	r := n.runner(*t)
	var commands commandRunner = r
	if n.runnerForTarget != nil {
		commands = n.runnerForTarget(*t)
	}
	run := func(capture bool, args ...string) ([]byte, error) {
		return commands.Exec(n.dir(*t), capture, "pulumi", args...)
	}
	list, err := run(true, "stack", "ls", "--json", "--non-interactive")
	exists := false
	if err == nil {
		exists, err = stackListedInProject(list, t.Stack, t.Project)
	}
	if err != nil {
		// A missing backend establishes only stack absence. Captured AWS IDs
		// below still have to be checked; a 403 never establishes absence.
		_, bucketErr := a.Call(nil, "s3api", "head-bucket", "--bucket", strings.TrimPrefix(t.Backend, "s3://"), "--expected-bucket-owner", n.receipt.Account)
		if !nukeMissing(bucketErr, "NoSuchBucket", "404", "NotFound") {
			n.failure(name, err)
			return
		}
	}
	if exists {
		pass := n.passphrase
		if t.Project == "attestra-index" {
			pass = n.sharedPassphrase
		}
		if err := ui.Input("Existing Pulumi passphrase for "+name+" (blank if using KMS)", &pass, true, false).Run(); err != nil {
			n.failure(name, err)
			return
		}
		if n.g != nil {
			if e := ensureNoNukeDeployments(n.g, n.receipt.Repository); e != nil {
				n.failure(name, e)
				return
			}
		}
		r.env = replaceEnvironment(r.env, map[string]string{"PULUMI_CONFIG_PASSPHRASE": pass})
		data, e := run(true, "stack", "export", "--stack", t.Stack)
		if e != nil {
			n.failure(name+" inventory", e)
			return
		}
		if e = validateNukeState(data, t.Project, t.Stack, n.receipt.Account); e != nil {
			n.failure(name+" ownership", e)
			return
		}
		if e = captureTeardownInventory(data, &t.Evidence); e != nil {
			n.failure(name+" inventory", e)
			return
		}
		if e = captureNukeDNS(data, &t.Evidence); e != nil {
			n.failure(name+" DNS inventory", e)
		}
		captureNukeRegions(data, t)
		if e = n.captureStateKey(data, t.Region); e != nil {
			n.failure(name+" secrets provider", e)
		}
		t.Evidence.InventoryCaptured = true
		if e = n.save(); e != nil {
			n.failure(name+" inventory persistence", e)
			return
		}
		for _, probe := range t.Evidence.Inventory {
			if probe.Retained {
				continue
			}
			pa := n.api(n.probeRegion(t, probe))
			e := verifyTeardownProbe(pa, probe)
			before[probe.Name+probe.ID] = errors.Is(e, errTeardownResourceRemains)
			if probe.Kind == "bucket" && before[probe.Name+probe.ID] {
				if e := purgeEvidence(pa, probe.ID, n.receipt.Account, t.Backend); e != nil {
					n.record(nukeResult{"Purge bucket " + probe.ID, "INFO", e.Error() + "; final resource verification decides success"})
				}
			}
			if probe.Kind == "table" && before[probe.Name+probe.ID] {
				if _, e := pa.Call(nil, "dynamodb", "update-table", "--table-name", probe.ID, "--no-deletion-protection-enabled"); e != nil {
					n.record(nukeResult{"Disable table deletion protection " + probe.ID, "INFO", e.Error() + "; final resource verification decides success"})
				}
			}
		}
		// Refresh reconciles already-missing resources. Its failure is reported,
		// but does not prevent a best-effort destroy of independent resources.
		if _, e = run(false, "refresh", "--stack", t.Stack, "--yes", "--skip-preview", "--non-interactive"); e != nil {
			n.record(nukeResult{name + " refresh", "INFO", e.Error() + "; final resource and stack verification decides success"})
		}
		current, e := run(true, "stack", "export", "--stack", t.Stack)
		if e != nil {
			n.failure(name+" refreshed inventory", e)
			return
		}
		if e = validateNukeState(current, t.Project, t.Stack, n.receipt.Account); e != nil {
			n.failure(name+" ownership", e)
			return
		}
		// The typed nuke confirmation authorizes removing deletion guards on
		// resources owned by these stacks. External/read resources stay external.
		prepared, changed, e := nukeDestroyableExport(current)
		if e != nil {
			n.failure(name, e)
			return
		}
		if changed {
			f, e := os.CreateTemp("", "attestra-nuke-state-*.json")
			if e != nil {
				n.failure(name, e)
				return
			}
			path := f.Name()
			_, writeErr := f.Write(prepared)
			closeErr := f.Close()
			if writeErr == nil && closeErr == nil {
				_, e = run(false, "stack", "import", "--stack", t.Stack, "--file", path, "--non-interactive")
			} else {
				e = errors.New("cannot prepare deletion guard update")
			}
			os.Remove(path)
			if e != nil {
				n.record(nukeResult{name + " deletion guards", "INFO", e.Error() + "; final resource and stack verification decides success"})
			}
		}
		if _, e = run(false, "destroy", "--stack", t.Stack, "--yes", "--skip-preview", "--continue-on-error", "--non-interactive"); e != nil {
			n.record(nukeResult{name + " destroy", "INFO", e.Error() + "; final resource and stack verification decides success"})
		}
		data, e = run(true, "stack", "export", "--stack", t.Stack)
		if e == nil {
			e = ensureDestroyed(data)
		}
		if e != nil {
			n.failure(name+" remaining Pulumi state", e)
		} else {
			n.record(nukeStep(name+" stack", func() (bool, error) {
				b, e := run(true, "stack", "ls", "--json", "--non-interactive")
				if e != nil {
					return false, e
				}
				return stackListedInProject(b, t.Stack, t.Project)
			}, func() error { _, e := run(false, "stack", "rm", t.Stack, "--yes", "--non-interactive"); return e }))
		}
	} else {
		n.record(nukeResult{name + " stack", "ABSENT", "stack/backend already absent"})
	}
}

func (n *nukeWorkflow) finishTarget(t *nukeTarget, before map[string]bool) {
	name := t.Project + "/" + t.Stack + " • " + t.Backend
	if !t.Evidence.InventoryCaptured {
		n.record(nukeResult{name + " AWS inventory", "UNVERIFIED", "stack is absent but no pre-destroy resource inventory survives; cannot certify AWS orphan cleanup"})
	}
	for _, probe := range t.Evidence.Inventory {
		label := name + " • " + probe.Name + " [" + probe.Kind + " " + probe.ID + "]"
		if probe.Retained {
			n.record(nukeResult{label, "RETAINED", "external or retainOnDelete resource; inspect ownership and remove manually"})
			continue
		}
		pa := n.api(n.probeRegion(t, probe))
		err := verifyTeardownProbe(pa, probe)
		for attempt := 0; errors.Is(err, errTeardownResourceRemains) && attempt < 2; attempt++ {
			time.Sleep(5 * time.Second)
			err = verifyTeardownProbe(pa, probe)
		}
		status, detail := "ABSENT", "already absent; verified"
		if before[probe.Name+probe.ID] {
			status, detail = "DELETED", "absence verified after destroy"
		}
		if errors.Is(err, errTeardownResourceRemains) {
			status, detail = "REMAINS", "AWS resource still exists"
		} else if err != nil {
			status, detail = "UNVERIFIED", err.Error()
		}
		n.record(nukeResult{label, status, detail})
		if probe.Kind == "lambda" && err == nil {
			n.cleanupLogGroup(n.probeRegion(t, probe), "/aws/lambda/"+probe.ID)
		}
		if probe.Kind == "table" && err == nil {
			n.cleanupTableBackups(n.probeRegion(t, probe), probe.ID)
		}
	}
	n.cleanupDNS(t)
}

// Keep every exported field (including encrypted secret envelopes), changing
// only the two deletion guards for managed resources. Never force-remove state.
func nukeDestroyableExport(data []byte) ([]byte, bool, error) {
	var state map[string]json.RawMessage
	if json.Unmarshal(data, &state) != nil {
		return nil, false, errors.New("invalid state")
	}
	var deployment map[string]json.RawMessage
	if json.Unmarshal(state["deployment"], &deployment) != nil {
		return nil, false, errors.New("invalid deployment")
	}
	var resources []map[string]json.RawMessage
	if json.Unmarshal(deployment["resources"], &resources) != nil {
		return nil, false, errors.New("invalid resources")
	}
	changed := false
	for _, r := range resources {
		if string(r["external"]) == "true" {
			continue
		}
		for _, key := range []string{"protect", "retainOnDelete"} {
			if string(r[key]) == "true" {
				r[key] = json.RawMessage("false")
				changed = true
			}
		}
	}
	deployment["resources"], _ = json.Marshal(resources)
	state["deployment"], _ = json.Marshal(deployment)
	b, err := json.Marshal(state)
	return b, changed, err
}

func captureNukeDNS(data []byte, p *teardownProgress) error {
	var state struct {
		Deployment struct {
			Resources []struct {
				Type, URN string
				External  bool
				Outputs   map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil {
		return errors.New("invalid DNS inventory")
	}
	// This also captures app associations, SES and API certificates.
	if p.Values == nil {
		p.Values = map[string]string{}
	}
	if err := captureTeardownEvidence(data, p); err != nil {
		return err
	}
	managed := map[string]bool{}
	managedNames := map[string]bool{}
	external := map[string]bool{}
	for _, r := range state.Deployment.Resources {
		if r.External {
			external[r.URN] = true
			continue
		}
		if r.Type == "aws:route53/record:Record" {
			var name, kind string
			_ = json.Unmarshal(r.Outputs["name"], &name)
			_ = json.Unmarshal(r.Outputs["type"], &kind)
			managed[dns.Name(name)+"/"+kind] = true
			managedNames[dns.Name(name)] = true
		}
		if r.Type == "aws:acm/certificate:Certificate" {
			var options []struct{ ResourceRecordName, ResourceRecordType, ResourceRecordValue string }
			if raw := r.Outputs["domainValidationOptions"]; len(raw) > 0 {
				if json.Unmarshal(raw, &options) != nil {
					return errors.New("invalid certificate DNS inventory")
				}
				for _, o := range options {
					p.Desired = append(p.Desired, dns.Record{Name: o.ResourceRecordName, Type: o.ResourceRecordType, Content: o.ResourceRecordValue})
				}
			}
		}
		if r.Type == "aws:apigatewayv2/domainName:DomainName" {
			var name string
			var configuration struct{ TargetDomainName string }
			_ = json.Unmarshal(r.Outputs["domainName"], &name)
			_ = json.Unmarshal(r.Outputs["domainNameConfiguration"], &configuration)
			if name != "" && configuration.TargetDomainName != "" {
				p.Desired = append(p.Desired, dns.Record{Name: name, Type: "CNAME", Content: configuration.TargetDomainName})
			}
		}
	}
	// retainOnDelete on managed resources is explicitly overridden by nuke.
	for i := range p.Inventory {
		p.Inventory[i].Retained = external[p.Inventory[i].Name]
	}
	wants := []dns.Record{}
	seen := map[string]bool{}
	for _, want := range p.Desired {
		key := dns.Name(want.Name) + "/" + want.Type
		if managed[key] || (want.Type == "CNAME" && managedNames[dns.Name(want.Name)]) || seen[key+want.Content] {
			continue
		}
		if want.Name == "" || want.Type == "" || want.Content == "" {
			return errors.New("incomplete DNS inventory")
		}
		seen[key+want.Content] = true
		wants = append(wants, want)
	}
	p.Desired = wants
	return nil
}

func (n *nukeWorkflow) cleanupDNS(t *nukeTarget) {
	for _, want := range t.Evidence.Desired {
		label := "DNS " + want.Type + " " + want.Name
		if n.cf == nil {
			n.record(nukeResult{label, "UNVERIFIED", "Cloudflare token unavailable; DNS not removed"})
			continue
		}
		zones, err := n.cf.Zones(want.Name)
		if err != nil {
			n.failure(label, err)
			continue
		}
		zone := ""
		for _, z := range zones {
			if dns.WithinZone(want.Name, z.Name) {
				zone = z.ID
				break
			}
		}
		if zone == "" {
			n.failure(label, errors.New("no accessible DNS zone"))
			continue
		}
		var matches []dns.Record
		n.record(nukeStep(label, func() (bool, error) {
			records, e := n.cf.Records(zone, want.Name)
			if e != nil {
				return false, e
			}
			matches, e = dns.DeletionCandidates(want, records)
			return len(matches) > 0, e
		}, func() error { return n.cf.DeleteDNS(zone, matches) }))
	}
}

func (n *nukeWorkflow) cleanupLogGroup(region, name string) {
	a := n.api(region)
	n.record(nukeStep("Log group "+name, func() (bool, error) {
		var result struct {
			LogGroups *[]struct{ LogGroupName string }
		}
		_, err := a.Call(&result, "logs", "describe-log-groups", "--log-group-name-prefix", name)
		if err != nil {
			return false, err
		}
		if result.LogGroups == nil {
			return false, errors.New("invalid log group response")
		}
		for _, g := range *result.LogGroups {
			if g.LogGroupName == name {
				return true, nil
			}
		}
		return false, nil
	}, func() error { _, err := a.Call(nil, "logs", "delete-log-group", "--log-group-name", name); return err }))
}

func captureNukeRegions(data []byte, t *nukeTarget) {
	var state struct {
		Deployment struct {
			Resources []struct {
				URN, ID, Type, Provider string
				Inputs                  map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil {
		return
	}
	providers := map[string]string{}
	for _, r := range state.Deployment.Resources {
		if r.Type == "pulumi:providers:aws" {
			var region string
			_ = json.Unmarshal(r.Inputs["region"], &region)
			if region != "" {
				providers[r.URN+"::"+r.ID] = region
			}
		}
	}
	if t.Regions == nil {
		t.Regions = map[string]string{}
	}
	for _, r := range state.Deployment.Resources {
		if region := providers[r.Provider]; region != "" {
			t.Regions[r.URN] = region
		}
	}
}

func (n *nukeWorkflow) probeRegion(t *nukeTarget, p teardownProbe) string {
	if region := t.Regions[p.Name]; region != "" {
		return region
	}
	return t.Region
}
