package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

type teardownProgress struct {
	Repository, Environment                                          string
	Values                                                           map[string]string
	Domain, Bucket, Zone                                             string
	Desired, DNS                                                     []dnsRecord
	Destroyed, DNSDone, DNSSkipped, RoleDone, StackRemoved, Complete bool
}

func (p *teardownProgress) save(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	// Keep a previous checkpoint if replacement is interrupted on Windows.
	temp := path + ".tmp"
	if err = os.WriteFile(temp, append(b, '\n'), 0600); err != nil {
		return err
	}
	if _, err = os.Stat(path); err == nil {
		if err = os.Rename(path, path+".bak"); err != nil {
			return err
		}
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	_ = os.Remove(path + ".bak")
	return nil
}
func typedConfirmation(label, expected string) error {
	answer := ""
	if err := input(label, &answer, false, true).Description("Type exactly: " + expected).Run(); err != nil {
		return err
	}
	if answer != expected {
		return errors.New("confirmation did not match; cancelled")
	}
	return nil
}
func captureTeardownEvidence(data []byte, p *teardownProgress) error {
	var state struct {
		Deployment *struct {
			Resources []struct {
				Type, URN, ID string
				Outputs       map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment == nil {
		return errors.New("invalid stack export")
	}
	var token string
	var dkim []string
	route53 := false
	for _, r := range state.Deployment.Resources {
		switch r.Type {
		case "aws:ses/domainIdentity:DomainIdentity":
			if !strings.HasSuffix(r.URN, "::email-sender") {
				continue
			}
			_ = json.Unmarshal(r.Outputs["domain"], &p.Domain)
			_ = json.Unmarshal(r.Outputs["verificationToken"], &token)
		case "aws:ses/domainDkim:DomainDkim":
			if strings.HasSuffix(r.URN, "::email-sender-dkim") {
				_ = json.Unmarshal(r.Outputs["dkimTokens"], &dkim)
			}
		case "aws:route53/record:Record":
			if strings.HasSuffix(r.URN, "::ses-verify") {
				route53 = true
			}
		case "aws:acm/certificate:Certificate":
			if strings.HasSuffix(r.URN, "::api-certificate") {
				var options []struct{ ResourceRecordName, ResourceRecordType, ResourceRecordValue string }
				if err := json.Unmarshal(r.Outputs["domainValidationOptions"], &options); err != nil {
					return errors.New("cannot capture API certificate DNS evidence")
				}
				for _, option := range options {
					if option.ResourceRecordName != "" && option.ResourceRecordType == "CNAME" {
						p.Desired = append(p.Desired, dnsRecord{Type: "CNAME", Name: strings.TrimSuffix(option.ResourceRecordName, "."), Content: option.ResourceRecordValue})
					}
				}
			}
		case "aws:apigatewayv2/domainName:DomainName":
			if strings.HasSuffix(r.URN, "::api-domain") {
				var name string
				var configuration struct{ TargetDomainName string }
				if json.Unmarshal(r.Outputs["domainName"], &name) != nil || json.Unmarshal(r.Outputs["domainNameConfiguration"], &configuration) != nil || name == "" || configuration.TargetDomainName == "" {
					return errors.New("cannot capture API domain DNS evidence")
				}
				p.Desired = append(p.Desired, dnsRecord{Type: "CNAME", Name: name, Content: configuration.TargetDomainName})
			}
		case "aws:s3/bucketV2:BucketV2":
			if strings.HasSuffix(r.URN, "::id-evidence") {
				p.Bucket = r.ID
			}
		}
	}
	if route53 {
		p.DNSDone = true
		return nil
	}
	if p.Domain != "" {
		if token != "" {
			p.Desired = append(p.Desired, dnsRecord{Type: "TXT", Name: "_amazonses." + p.Domain, Content: token})
		}
		for _, t := range dkim {
			p.Desired = append(p.Desired, dnsRecord{Type: "CNAME", Name: t + "._domainkey." + p.Domain, Content: t + ".dkim.amazonses.com"})
		}
	}
	return nil
}
func runTeardown(root string) error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("teardown requires an interactive terminal; there is no unattended destroy mode")
	}
	if err := (&bootstrapWizard{root: root}).preflight(); err != nil {
		return err
	}
	fmt.Println("Attestra • Teardown environment\nPermanent removal of application data, approved SES/API DNS records, deployment role, stack, and GitHub environment. Stop deployments for this repository until finished. S3 state bucket and shared OIDC provider are retained.")
	repo, environment, token := "lambdawalker/go.attestra.aws.auth", "dev", ""
	if err := huh.NewForm(huh.NewGroup(input("GitHub repository", &repo, false, true).Validate(func(s string) error {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(s) {
			return errors.New("use owner/repository")
		}
		return nil
	}), input("Environment to tear down", &environment, false, true).Validate(validateEnvironment), input("GitHub token (hidden; Administration/Environments write and Actions read)", &token, true, true))).Run(); err != nil {
		return err
	}
	g := newGitHubClient(strings.TrimSpace(token))
	var metadata repositoryMetadata
	found, err := g.request("GET", "/repos/"+repo, nil, &metadata)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("repository unavailable")
	}
	if err = g.ensureNoDeployments(repo); err != nil {
		return err
	}
	path := filepath.Join(root, "teardown."+environment+".local.json")
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("teardown is locked; if a previous process crashed, verify it is stopped before removing " + lockPath)
	}
	fmt.Fprintf(lock, "pid %d\n", os.Getpid())
	lock.Close()
	defer os.Remove(lockPath)
	p := teardownProgress{Repository: repo, Environment: environment}
	data, err := os.ReadFile(path)
	resume := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if resume {
		if json.Unmarshal(data, &p) != nil || p.Repository != repo || p.Environment != environment {
			return errors.New("teardown checkpoint does not match repository/environment")
		}
		if p.Complete {
			return errors.New("this teardown is already complete; archive its local progress file before tearing down a recreated environment")
		}
	} else if _, e := os.Stat(path + ".bak"); e == nil {
		return errors.New("interrupted checkpoint replacement; restore the .bak checkpoint before continuing")
	}
	previous, err := g.inspectEnvironment(repo, environment)
	if err != nil {
		return err
	}
	if !resume {
		if !previous.Exists {
			return errors.New("GitHub environment not found; a saved teardown checkpoint is required to resume")
		}
		p.Values = previous.Variables
	} else if previous.Exists {
		for _, key := range environmentVariables {
			if previous.Variables[key] != p.Values[key] {
				return errors.New("GitHub environment changed since teardown started; inspect before resuming")
			}
		}
	}
	if err = validateSetupValues(p.Values); err != nil {
		return err
	}
	if p.Values["PULUMI_STACK"] != environment {
		return errors.New("stack/environment mismatch")
	}
	if err = g.checkTeardownSharing(repo, environment, p.Values); err != nil {
		return err
	}
	o := options{Root: root, Stack: environment, Backend: p.Values["PULUMI_BACKEND_URL"], Region: p.Values["AWS_REGION"], Profile: "attestra"}
	mode := "sso"
	if err = huh.NewSelect[string]().Title("AWS administrator authentication").Options(huh.NewOption("SSO", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Access key credentials", "keys")).Value(&mode).Run(); err != nil {
		return err
	}
	c := credentials{}
	if mode == "keys" {
		if err = huh.NewForm(huh.NewGroup(input("AWS access key ID", &c.Access, true, true), input("AWS secret key", &c.Secret, true, true), input("AWS session token", &c.Token, true, false))).Run(); err != nil {
			return err
		}
	} else {
		if err = input("AWS profile", &o.Profile, false, true).Run(); err != nil {
			return err
		}
		o.Sso = mode == "sso"
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return err
		}
	}
	if err = input("Existing Pulumi stack passphrase", &c.Passphrase, true, true).Run(); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "attestra-teardown-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	empty := filepath.Join(temporary, "aws-config")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env := cloudEnvironment(os.Environ(), c, o, empty)
	r := &processRunner{env: env}
	a := &setupAWSClient{env: env}
	var identity struct{ Account, Arn string }
	if _, err = a.call(&identity, "sts", "get-caller-identity"); err != nil {
		return err
	}
	if identity.Account != p.Values["AWS_ACCOUNT_ID"] {
		return errors.New("AWS account does not match selected environment")
	}
	// Teardown must not run as the role it will delete.
	roleName := strings.Split(p.Values["AWS_ROLE_ARN"], "/")
	if strings.Contains(identity.Arn, ":assumed-role/"+roleName[len(roleName)-1]+"/") {
		return errors.New("use an administrator profile, not the deployment role being deleted")
	}
	if err = checkBucket(r, o, identity.Account); err != nil {
		return err
	}
	infra := filepath.Join(root, "infra")
	list, err := r.Exec(infra, true, "pulumi", "stack", "ls", "--json", "--non-interactive")
	if err != nil {
		return err
	}
	exists, err := stackListed(list, environment)
	if err != nil {
		return err
	}
	if !exists && !p.Destroyed {
		return errors.New("stack missing without a successful destroy checkpoint; refusing external cleanup")
	}
	if exists && p.StackRemoved {
		return errors.New("stack was recreated after teardown; refusing to remove it")
	}
	if exists {
		if _, err = r.Exec(infra, true, "pulumi", "stack", "select", environment, "--non-interactive"); err != nil {
			return err
		}
		if !p.Destroyed {
			if err = checkStack(r, o); err != nil {
				return err
			}
		}
		data, err = r.Exec(infra, true, "pulumi", "stack", "export", "--stack", environment)
		if err != nil {
			return err
		}
		if p.Destroyed {
			if err = ensureDestroyed(data); err != nil {
				return err
			}
		} else if !resume {
			if err = captureTeardownEvidence(data, &p); err != nil {
				return err
			}
		}
	}
	if !p.RoleDone {
		if _, _, err = inspectTeardownRole(a, p.Values["AWS_ROLE_ARN"], environment, metadata); err != nil {
			return err
		}
	}
	var cf *cloudflareClient
	if !p.DNSDone && !p.DNSSkipped && len(p.Desired) > 0 {
		cleanup := true
		if err = huh.NewConfirm().Title("Clean up this environment's SES/API DNS records in Cloudflare?").Description("Choose No to skip Cloudflare, keep its DNS records, and continue removing AWS and GitHub resources.").Affirmative("Clean up DNS").Negative("Skip Cloudflare").Value(&cleanup).Run(); err != nil {
			return err
		}
		p.DNSSkipped = !cleanup
	}
	if !p.DNSDone && !p.DNSSkipped && len(p.Desired) > 0 {
		cfToken := ""
		if err = input("Cloudflare token (hidden; Zone Read + DNS Edit)", &cfToken, true, true).Run(); err != nil {
			return err
		}
		cf = newCloudflareClient(strings.TrimSpace(cfToken))
		zones, e := cf.zones(p.Domain)
		if e != nil {
			return e
		}
		eligible := zones[:0]
		for _, z := range zones {
			coversAll := true
			for _, record := range p.Desired {
				if !withinZone(record.Name, z.Name) {
					coversAll = false
				}
			}
			if coversAll {
				eligible = append(eligible, z)
			}
		}
		zones = eligible
		if p.Zone == "" {
			if len(zones) == 0 {
				return errors.New("no matching active Cloudflare zone; no removals performed")
			}
			choices := []huh.Option[string]{}
			for _, z := range zones {
				choices = append(choices, huh.NewOption(z.Name+" ("+z.ID+")", z.ID))
			}
			p.Zone = zones[0].ID
			if err = huh.NewSelect[string]().Title("Cloudflare zone to clean up").Options(choices...).Value(&p.Zone).Run(); err != nil {
				return err
			}
			for _, want := range p.Desired {
				current, e := cf.records(p.Zone, want.Name)
				if e != nil {
					return e
				}
				matches, e := dnsDeletionCandidates(want, current)
				if e != nil {
					return e
				}
				p.DNS = append(p.DNS, matches...)
			}
		}
		valid := false
		for _, z := range zones {
			if z.ID == p.Zone {
				valid = true
			}
		}
		if !valid {
			return errors.New("saved Cloudflare zone is no longer accessible")
		}
	}
	fmt.Printf("\nRepository: %s\nEnvironment/stack: %s\nAWS account: %s\nRegion: %s\nState backend (retained): %s\nRole: %s\nEvidence bucket: %s\n", repo, environment, identity.Account, o.Region, o.Backend, p.Values["AWS_ROLE_ARN"], p.Bucket)
	if p.DNSSkipped {
		fmt.Println("SKIP Cloudflare: remaining SES/API DNS records will be retained. Their expected values are saved in the teardown progress file for manual cleanup.")
		for _, record := range p.Desired {
			fmt.Printf("RETAIN DNS %s %s = %s\n", record.Type, record.Name, record.Content)
		}
	} else {
		for _, record := range p.DNS {
			fmt.Printf("DELETE DNS %s %s = %s [id %s]\n", record.Type, record.Name, record.Content, record.ID)
		}
	}
	fmt.Println("Only approve these DNS records if no other environment/service still uses this SES identity. Do not start local or GitHub deployments during teardown.")
	if !p.Destroyed {
		if _, err = r.Exec(infra, false, "pulumi", "destroy", "--stack", environment, "--preview-only", "--non-interactive"); err != nil {
			return err
		}
	}
	if err = typedConfirmation("Type the environment name to permanently tear it down", environment); err != nil {
		return err
	}
	if err = p.save(path); err != nil {
		return err
	}
	persist := func() error { return p.save(path) }
	return runTeardownSteps([]teardownStep{
		{"Pulumi destroy", func() error {
			if p.Destroyed {
				return nil
			}
			if err := g.ensureNoDeployments(repo); err != nil {
				return err
			}
			if p.Bucket != "" {
				purge := false
				if err := huh.NewConfirm().Title("Permanently purge all ID evidence versions in " + p.Bucket + "? Required if this application bucket is nonempty.").Value(&purge).Run(); err != nil {
					return err
				}
				if purge {
					if err := typedConfirmation("Type the evidence bucket name to authorize permanent data deletion", p.Bucket); err != nil {
						return err
					}
					if err := purgeEvidence(a, p.Bucket, identity.Account, o.Backend); err != nil {
						return err
					}
				}
			}
			if _, err := r.Exec(infra, false, "pulumi", "destroy", "--stack", environment, "--yes", "--non-interactive"); err != nil {
				return err
			}
			data, err := r.Exec(infra, true, "pulumi", "stack", "export", "--stack", environment)
			if err != nil {
				return err
			}
			if err = ensureDestroyed(data); err != nil {
				return err
			}
			p.Destroyed = true
			return persist()
		}},
		{"Cloudflare cleanup", func() error { return cleanupTeardownDNS(&p, cf, persist) }},
		{"Deployment role cleanup", func() error {
			if p.RoleDone {
				return nil
			}
			if err := g.ensureNoDeployments(repo); err != nil {
				return err
			}
			if err := deleteTeardownRole(a, p.Values["AWS_ROLE_ARN"], environment, metadata); err != nil {
				return err
			}
			p.RoleDone = true
			return persist()
		}},
		{"Pulumi stack removal", func() error {
			if p.StackRemoved {
				return nil
			}
			if exists {
				yaml := filepath.Join(infra, "Pulumi."+environment+".yaml")
				if b, e := os.ReadFile(yaml); e == nil {
					if e = os.WriteFile(yaml+".bak.teardown."+time.Now().UTC().Format("20060102T150405.000000000"), b, 0600); e != nil {
						return e
					}
				} else if !os.IsNotExist(e) {
					return e
				}
				if _, err := r.Exec(infra, false, "pulumi", "stack", "rm", environment, "--yes", "--non-interactive"); err != nil {
					return err
				}
			}
			p.StackRemoved = true
			return persist()
		}},
		{"GitHub environment cleanup", func() error {
			if err := g.ensureNoDeployments(repo); err != nil {
				return err
			}
			now, err := g.inspectEnvironment(repo, environment)
			if err != nil {
				return err
			}
			if now.Exists {
				for _, key := range environmentVariables {
					if now.Variables[key] != p.Values[key] {
						return errors.New("GitHub settings changed; cleanup stopped")
					}
				}
				if _, err = g.request("DELETE", environmentPath(repo, environment), nil, nil); err != nil {
					return err
				}
			}
			found, err := g.request("GET", environmentPath(repo, environment), nil, nil)
			if err != nil {
				return err
			}
			if found {
				return errors.New("GitHub environment deletion not verified")
			}
			p.Complete = true
			if err = persist(); err != nil {
				return err
			}
			if p.DNSSkipped {
				fmt.Println("Cloudflare cleanup was skipped; remaining SES/API DNS records need manual cleanup. See Desired/DNS in the teardown progress file.")
			}
			fmt.Println("✓ Teardown complete. State bucket/history, shared OIDC provider, Cloudflare zone and unrelated records were retained. Review and commit removal of the stack YAML. Keep the teardown progress file and encrypted backup privately.")
			return nil
		}},
	})
}

// A skipped phase remains distinct from a successfully completed DNS deletion.
func cleanupTeardownDNS(p *teardownProgress, cf *cloudflareClient, persist func() error) error {
	if p.DNSDone || p.DNSSkipped {
		return nil
	}
	if len(p.DNS) > 0 {
		if cf == nil {
			return errors.New("Cloudflare client missing for approved DNS cleanup")
		}
		if err := cf.deleteDNS(p.Zone, p.DNS); err != nil {
			return err
		}
	}
	p.DNSDone = true
	return persist()
}
