package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func runIndexTeardown(root string) error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("shared index teardown requires an interactive terminal")
	}
	if err := (&bootstrapWizard{root: root}).preflight(); err != nil {
		return err
	}
	fmt.Println("Attestra • Teardown shared index")
	fmt.Println("Tear down every environment first. Stop local and GitHub deployments until finished. Removes the shared API, Lambda, registry, certificate and approved DNS records. State bucket/history, vaults, GitHub environments and shared OIDC provider are retained.")
	o := options{Root: root, Stack: "shared", Region: "us-east-2", Profile: "attestra"}
	if err := ui.Input("AWS region containing the shared index", &o.Region, false, true).Run(); err != nil {
		return err
	}
	vaultEnv := ""
	if err := ui.Input("Environment whose credential vault to unlock (optional)", &vaultEnv, false, false).Run(); err != nil {
		return err
	}
	saved := &bootstrapWizard{root: root, environment: strings.TrimSpace(vaultEnv)}
	defer saved.cleanup()
	if saved.environment != "" {
		if err := ui.ValidateEnvironment(saved.environment); err != nil {
			return err
		}
		repo := teardownRepository(root, saved.environment)
		if repo == "" {
			repo = "lambdawalker/go.attestra.aws.auth"
		}
		if err := saved.loadCredentials(repo, false); err != nil {
			return err
		}
	}
	mode := "sso"
	if err := huh.NewSelect[string]().Title("AWS administrator authentication").Options(huh.NewOption("SSO", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Access key credentials", "keys")).Value(&mode).Run(); err != nil {
		return err
	}
	c := credentials{Access: saved.secrets.AWSAccess, Secret: saved.secrets.AWSSecret, Token: saved.secrets.AWSToken}
	var err error
	if mode == "keys" {
		if c.Access == "" || c.Secret == "" {
			if err = huh.NewForm(huh.NewGroup(ui.Input("AWS access key ID", &c.Access, true, true), ui.Input("AWS secret access key", &c.Secret, true, true), ui.Input("AWS session token", &c.Token, true, false))).Run(); err != nil {
				return err
			}
		}
	} else {
		if err = ui.Input("AWS profile", &o.Profile, false, true).Run(); err != nil {
			return err
		}
		o.Sso, o.Login = mode == "sso", mode == "login"
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return err
		}
	}
	c.Passphrase = saved.secrets.IndexPulumi
	if c.Passphrase == "" {
		if err = ui.Input("Existing shared index Pulumi passphrase", &c.Passphrase, true, true).Run(); err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "attestra-index-teardown-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	empty := filepath.Join(tmp, "aws-config")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env, err := prepareCloudEnvironment(os.Environ(), c, o, empty)
	if err != nil {
		return err
	}
	a := &awsenv.Client{Env: env, RenewalFailure: credentialRenewalFailure}
	var identity struct{ Account, Arn string }
	if _, err = a.Call(&identity, "sts", "get-caller-identity"); err != nil {
		return err
	}
	if len(identity.Account) != 12 {
		return errors.New("invalid AWS account response")
	}
	o.Backend = "s3://attestra-index-state-" + identity.Account + "-" + o.Region
	if err = o.validate(); err != nil {
		return err
	}
	r := &processRunner{env: replaceEnvironment(env, map[string]string{"PULUMI_BACKEND_URL": o.backendURL()})}
	if err = checkBucket(r, o, identity.Account); err != nil {
		return err
	}
	path := filepath.Join(root, ".attestra", "teardown-index."+identity.Account+"."+o.Region+".local.json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("index teardown is locked; inspect %s.lock before retrying", path)
	}
	fmt.Fprintf(lock, "pid %d\n", os.Getpid())
	lock.Close()
	defer os.Remove(path + ".lock")
	p := teardownProgress{Environment: "shared", Values: map[string]string{"account": identity.Account, "region": o.Region, "backend": o.Backend}}
	if data, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(data, &p) != nil || p.Values["account"] != identity.Account || p.Values["region"] != o.Region || p.Values["backend"] != o.Backend {
			return errors.New("shared teardown checkpoint identity mismatch")
		}
		if p.Complete {
			return errors.New("this shared index teardown is complete; archive its checkpoint before tearing down a recreated index")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if _, e = os.Stat(path + ".bak"); e == nil {
		return errors.New("restore interrupted index teardown checkpoint from .bak before continuing")
	}
	persist := func() error { return p.save(path) }
	dir := filepath.Join(root, "infra-index")
	run := func(capture bool, args ...string) ([]byte, error) { return r.Exec(dir, capture, "pulumi", args...) }
	// Same remote lock as bootstrap. This prevents setup from racing teardown.
	bucket := strings.TrimPrefix(o.Backend, "s3://")
	body := filepath.Join(tmp, "lock.json")
	if err = os.WriteFile(body, []byte(`{"operation":"teardown-index"}`), 0600); err != nil {
		return err
	}
	var lease struct{ ETag string }
	if _, err = a.Call(&lease, "s3api", "put-object", "--bucket", bucket, "--key", "bootstrap.lock", "--body", body, "--if-none-match", "*", "--expected-bucket-owner", identity.Account); err != nil {
		return fmt.Errorf("shared index setup/teardown lock unavailable: %w", err)
	}
	if lease.ETag == "" {
		return errors.New("bootstrap lock missing ETag; inspect it before retrying")
	}
	defer func() {
		if _, e := a.Call(nil, "s3api", "delete-object", "--bucket", bucket, "--key", "bootstrap.lock", "--if-match", lease.ETag, "--expected-bucket-owner", identity.Account); e != nil {
			fmt.Println("Could not release shared index bootstrap.lock; inspect it before retrying.")
		}
	}()
	if !p.InventoryCaptured {
		if err = checkIndexTeardownPending(a, bucket, identity.Account); err != nil {
			return fmt.Errorf("restore the original local teardown checkpoint before resuming: %w", err)
		}
	}
	list, err := run(true, "stack", "ls", "--json", "--non-interactive")
	if err != nil {
		return err
	}
	exists, err := stackListedInProject(list, "shared", "attestra-index")
	if err != nil {
		return err
	}
	if !exists && !p.Destroyed {
		return errors.New("shared stack missing without a completed destroy checkpoint; restore its backend before proceeding")
	}
	if exists && !p.Destroyed {
		if _, err = run(true, "config", "refresh", "--stack", "shared", "--force", "--non-interactive"); err != nil {
			return err
		}
		cfgData, e := run(true, "config", "--json", "--stack", "shared")
		if e != nil {
			return e
		}
		var cfg map[string]struct{ Value string }
		if json.Unmarshal(cfgData, &cfg) != nil {
			return errors.New("invalid index config")
		}
		for k, v := range cfg {
			if strings.HasPrefix(k, "aws:") && (k != "aws:region" || v.Value != o.Region) {
				return errors.New("index AWS provider overrides require manual review")
			}
		}
		if cfg["aws:region"].Value != o.Region {
			return errors.New("index region mismatch")
		}
		if p.InventoryCaptured && (p.Domain != cfg["attestra-index:domain"].Value || p.Values["route53ZoneId"] != cfg["attestra-index:route53ZoneId"].Value) {
			return errors.New("index domain/DNS configuration changed since teardown started")
		}
		p.Domain = cfg["attestra-index:domain"].Value
		if err = validateBootstrapApp("https://"+p.Domain, p.Domain, "verify@"+p.Domain); err != nil {
			return err
		}
		p.Values["route53ZoneId"] = cfg["attestra-index:route53ZoneId"].Value
		data, e := run(true, "stack", "export", "--stack", "shared")
		if e != nil {
			return e
		}
		if p.InventoryCaptured {
			current := teardownProgress{Values: map[string]string{}}
			if err = captureIndexTeardown(data, &current); err != nil {
				return err
			}
			known := map[string]bool{}
			for _, probe := range p.Inventory {
				known[probe.Name+"\x00"+probe.ID] = true
			}
			for _, probe := range current.Inventory {
				if !known[probe.Name+"\x00"+probe.ID] {
					return errors.New("index resource changed since teardown started; review before deleting")
				}
			}
		}
		if !p.InventoryCaptured {
			if err = captureIndexTeardown(data, &p); err != nil {
				return err
			}
			output, e := run(true, "stack", "output", "--json", "--stack", "shared")
			if e != nil {
				return e
			}
			var out map[string]string
			if json.Unmarshal(output, &out) != nil {
				return errors.New("invalid index outputs")
			}
			if target := out["domainTarget"]; target != "" {
				p.Desired = append(p.Desired, dns.Record{Type: "CNAME", Name: p.Domain, Content: target})
			}
			if p.Values["route53ZoneId"] != "" {
				p.Desired = nil
			}
		}
		if err = checkIndexRegistry(a, p.Bucket, p.IndexRetired); err != nil {
			return err
		}
	}
	var cf *dns.Client
	if len(p.Desired) > 0 {
		if err = saved.ensureCloudflare(); err != nil {
			return err
		}
		cf = saved.cf
		if p.Zone == "" {
			zones, e := cf.Zones(p.Domain)
			if e != nil {
				return e
			}
			var choices []huh.Option[string]
			for _, z := range zones {
				covers := true
				for _, want := range p.Desired {
					covers = covers && dns.WithinZone(want.Name, z.Name)
				}
				if covers {
					choices = append(choices, huh.NewOption(z.Name+" ("+z.ID+")", z.ID))
				}
			}
			if len(choices) == 0 {
				return errors.New("no Cloudflare zone covers the index records")
			}
			if err = huh.NewSelect[string]().Title("Cloudflare zone to clean up").Options(choices...).Value(&p.Zone).Run(); err != nil {
				return err
			}
			for _, want := range p.Desired {
				current, e := cf.Records(p.Zone, want.Name)
				if e != nil {
					return e
				}
				matches, e := dns.DeletionCandidates(want, current)
				if e != nil {
					return e
				}
				p.DNS = append(p.DNS, matches...)
			}
		}
	}
	fmt.Printf("\nAccount: %s\nIdentity: %s\nRegion: %s\nStack: attestra-index/shared\nDomain: %s\nRegistry table: %s\nState bucket retained: %s\n", identity.Account, identity.Arn, o.Region, p.Domain, p.Bucket, o.Backend)
	for _, probe := range p.Inventory {
		fmt.Println("DELETE", probe.Kind, probe.ID)
	}
	for _, record := range p.DNS {
		fmt.Printf("DELETE DNS %s %s = %s\n", record.Type, record.Name, record.Content)
	}
	fmt.Println("ACM validation records may be reused by another certificate for this exact hostname. Confirm they are exclusive to this index. Keep all deployment processes stopped until teardown finishes.")
	if err = typedConfirmation("Authorize permanent shared index deletion; all environments must already be torn down", "delete index "+identity.Account+" "+o.Region); err != nil {
		return err
	}
	if err = persist(); err != nil {
		return err
	}
	// Persistent remote marker prevents a new setup after interrupted destruction.
	if _, err = a.Call(nil, "s3api", "put-object", "--bucket", bucket, "--key", "teardown-pending.json", "--body", body, "--expected-bucket-owner", identity.Account); err != nil {
		return err
	}
	if err = destroyIndexStack(r, a, dir, &p, persist); err != nil {
		return err
	}
	if err = cleanupTeardownDNS(&p, cf, persist); err != nil {
		return err
	}
	if err = finalizeTeardownValidation(&p, persist, func() teardownReport { return verifyIndexTeardown(a, cf, &p) }, time.Sleep); err != nil {
		return err
	}
	if exists {
		yaml := filepath.Join(dir, "Pulumi.shared.yaml")
		if b, e := os.ReadFile(yaml); e == nil {
			if err = os.WriteFile(filepath.Join(root, ".attestra", "index-config."+identity.Account+"."+o.Region+".yaml.bak"), b, 0600); err != nil {
				return err
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if _, err = run(false, "stack", "rm", "shared", "--yes", "--non-interactive"); err != nil {
			return err
		}
	}
	list, err = run(true, "stack", "ls", "--json", "--non-interactive")
	if err != nil {
		return err
	}
	remains, err := stackListedInProject(list, "shared", "attestra-index")
	if err != nil {
		return err
	}
	if remains {
		return errors.New("shared stack still exists after removal")
	}
	for _, key := range []string{"bootstrap-ready.json", "teardown-pending.json"} {
		if _, err = a.Call(nil, "s3api", "delete-object", "--bucket", bucket, "--key", key, "--expected-bucket-owner", identity.Account); err != nil {
			return err
		}
	}
	if err = os.Remove(filepath.Join(dir, "Pulumi.shared.yaml")); err != nil && !os.IsNotExist(err) {
		return err
	}
	p.Complete = true
	if err = persist(); err != nil {
		return err
	}
	fmt.Println("✓ Shared index teardown verified. State bucket/history, encrypted configuration backup, credential vaults, GitHub settings and OIDC provider retained. Rerun setup to create a new index.")
	return nil
}

func verifyIndexTeardown(a awsenv.API, cf *dns.Client, p *teardownProgress) teardownReport {
	var report teardownReport
	for _, probe := range p.Inventory {
		status, detail := "DELETED", "absence verified"
		if err := verifyTeardownProbe(a, probe); err != nil {
			status, detail = "FAILED", err.Error()
			if errors.Is(err, errTeardownResourceRemains) {
				status = "REMAINS"
			}
		}
		report = append(report, teardownResult{probe.Name, status, detail})
	}
	for _, want := range p.Desired {
		if cf == nil {
			report = append(report, teardownResult{want.Name, "FAILED", "Cloudflare client missing"})
			continue
		}
		current, err := cf.Records(p.Zone, want.Name)
		status, detail := "DELETED", "record absence verified"
		if err != nil {
			status, detail = "FAILED", err.Error()
		} else {
			matches, e := dns.DeletionCandidates(want, current)
			if e != nil {
				status, detail = "FAILED", e.Error()
			} else if len(matches) > 0 {
				status, detail = "REMAINS", "index DNS record still exists"
			}
		}
		report = append(report, teardownResult{want.Name, status, detail})
	}
	report = append(report, teardownResult{"Recovery and shared services", "RETAINED", "state bucket/history, credential vaults, GitHub settings, OIDC provider and service-created logs/backups"})
	return report
}

func checkIndexTeardownPending(a awsenv.API, bucket, account string) error {
	_, err := a.Call(nil, "s3api", "head-object", "--bucket", bucket, "--key", "teardown-pending.json", "--expected-bucket-owner", account)
	if err == nil {
		return errors.New("shared index teardown is unfinished; rerun teardown-index before setup")
	}
	var ae *awsenv.Error
	if errors.As(err, &ae) && (ae.Code == "404" || ae.Code == "NoSuchKey" || ae.Code == "NotFound") {
		return nil
	}
	return fmt.Errorf("cannot check shared index teardown marker: %w", err)
}

func archiveCompletedIndexTeardown(root, account, region string) error {
	path := filepath.Join(root, ".attestra", "teardown-index."+account+"."+region+".local.json")
	for _, suffix := range []string{".bak", ".tmp", ".lock"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			return fmt.Errorf("inspect interrupted index teardown file before setup: %s", path+suffix)
		}
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p teardownProgress
	if json.Unmarshal(data, &p) != nil || p.Values["account"] != account || p.Values["region"] != region {
		return errors.New("invalid index teardown checkpoint")
	}
	if !p.Complete {
		return errors.New("finish shared index teardown before setup")
	}
	history := filepath.Join(root, ".attestra", "teardown-history")
	if err = os.MkdirAll(history, 0700); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(history, filepath.Base(path)+"."+time.Now().UTC().Format("20060102T150405.000000000")))
}
