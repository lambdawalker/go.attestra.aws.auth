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
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

type nukeTarget struct {
	Backend, Region, Project, Stack string
	Evidence                        teardownProgress
	Regions                         map[string]string
}

type nukeBackend struct{ Bucket, Region string }
type nukeKey struct{ ID, Region string }
type nukeReceipt struct {
	Repository, Account, UpdatedAt string
	Targets                        []nukeTarget
	Backends                       []nukeBackend
	Keys                           []nukeKey
	Results                        []nukeResult
	Environments                   []string
	CheckedAt                      string       `json:",omitempty"`
	CheckResults                   []nukeResult `json:",omitempty"`
}

type nukeWorkflow struct {
	root, path string
	env        []string
	g          *github.Client
	cf         *dns.Client
	metadata   repositoryMetadata
	receipt    nukeReceipt
	// Secrets are session-only and never serialized in a receipt.
	passphrase, sharedPassphrase string
	persistErr                   error
	awsForRegion                 func(string) awsenv.API
	confirmKey                   func(string, string) error
	runnerForTarget              func(nukeTarget) commandRunner
}

func (n *nukeWorkflow) save() error {
	n.receipt.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(n.receipt, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(n.path), "nuke-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Windows cannot rename over an existing file. Keep the old checkpoint
	// until the new one is fully written; retain .bak if replacement fails.
	if _, err = os.Stat(n.path); err == nil {
		if err = os.Rename(n.path, n.path+".bak"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(f.Name(), n.path); err != nil {
		return err
	}
	if err = os.Remove(n.path + ".bak"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (n *nukeWorkflow) record(r nukeResult) {
	fmt.Printf("%s • %s: %s\n", r.Status, r.Name, r.Detail)
	n.receipt.Results = append(n.receipt.Results, r)
	if err := n.save(); err != nil {
		n.persistErr = err
		fmt.Println("FAILED • cannot persist nuke report; recovery cleanup is blocked")
	}
}

func (n *nukeWorkflow) failure(name string, err error) {
	n.record(nukeResult{name, "FAILED", err.Error()})
}

func (n *nukeWorkflow) api(region string) awsenv.API {
	if n.awsForRegion != nil {
		return n.awsForRegion(region)
	}
	return &awsenv.Client{Env: replaceEnvironment(n.env, map[string]string{"AWS_REGION": region, "AWS_DEFAULT_REGION": region}), RenewalFailure: credentialRenewalFailure}
}

func (n *nukeWorkflow) runner(t nukeTarget) *processRunner {
	pass := n.passphrase
	if t.Project == "attestra-index" {
		pass = n.sharedPassphrase
	}
	o := options{Backend: t.Backend, Region: t.Region}
	return &processRunner{env: replaceEnvironment(n.env, map[string]string{"AWS_REGION": t.Region, "AWS_DEFAULT_REGION": t.Region, "PULUMI_BACKEND_URL": o.backendURL(), "PULUMI_CONFIG_PASSPHRASE": pass})}
}

func (n *nukeWorkflow) dir(t nukeTarget) string {
	if t.Project == "attestra-index" {
		return filepath.Join(n.root, "infra-index")
	}
	return filepath.Join(n.root, "infra")
}

func runNukeTeardown(root string) error {
	return runNukeMode(root, false)
}

func runNukeMode(root string, check bool) error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("nuke teardown requires an interactive terminal")
	}
	if !check {
		if err := (&bootstrapWizard{root: root}).preflight(); err != nil {
			return err
		}
	}
	if check {
		fmt.Println("Attestra • Post-nuke check of the last report (no deletions)")
	} else {
		fmt.Println("Attestra • Nuke all environments and shared index/admin")
		fmt.Println("Stop ALL local and GitHub deployments. This removes application data and all S3 object versions. Recovery state buckets and customer-managed KMS keys are processed LAST, only if every earlier cleanup is verified. Missing items are success. Shared OIDC providers and DNS zones are outside this project's ownership.")
	}
	repo := localRepository(root)
	if repo == "" {
		repo = "lambdawalker/go.attestra.aws.auth"
	}
	if err := ui.Input("GitHub repository", &repo, false, true).Run(); err != nil {
		return err
	}
	if len(strings.Split(repo, "/")) != 2 || strings.ContainsAny(repo, " ?#\r\n") {
		return errors.New("use owner/repository")
	}
	vaultEnv := "dev"
	if err := ui.Input("Environment credential vault to unlock (blank to enter credentials)", &vaultEnv, false, false).Run(); err != nil {
		return err
	}
	w := &bootstrapWizard{root: root, environment: vaultEnv}
	defer w.cleanup()
	if vaultEnv != "" {
		if err := ui.ValidateEnvironment(vaultEnv); err != nil {
			return err
		}
		if err := w.loadCredentials(repo, false); err != nil {
			return err
		}
	}
	token := w.secrets.GitHub
	if token == "" {
		prompt := "GitHub token (Administration/Environments write, Actions read)"
		if check {
			prompt = "GitHub read token (blank leaves GitHub artifacts unverifiable)"
		}
		if err := ui.Input(prompt, &token, true, !check).Run(); err != nil {
			return err
		}
	}
	var g *github.Client
	if strings.TrimSpace(token) != "" {
		g = github.NewClient(strings.TrimSpace(token))
	}
	var metadata repositoryMetadata
	var err error
	if !check {
		found, e := g.Request("GET", "/repos/"+repo, nil, &metadata)
		if e != nil {
			return e
		}
		if !found {
			return errors.New("repository unavailable")
		}
		if err = ensureNoNukeDeployments(g, repo); err != nil {
			return err
		}
	}
	o := options{Root: root, Stack: "nuke", Backend: "s3://attestra-nuke-discovery", Region: "us-east-2", Profile: "attestra"}
	mode := "sso"
	if err = huh.NewSelect[string]().Title("AWS administrator authentication").Options(huh.NewOption("SSO", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Access key credentials", "keys")).Value(&mode).Run(); err != nil {
		return err
	}
	c := credentials{Access: w.secrets.AWSAccess, Secret: w.secrets.AWSSecret, Token: w.secrets.AWSToken}
	if mode == "keys" {
		if c.Access == "" || c.Secret == "" {
			if err = huh.NewForm(huh.NewGroup(ui.Input("AWS access key ID", &c.Access, true, true), ui.Input("AWS secret access key", &c.Secret, true, true), ui.Input("AWS session token", &c.Token, true, false))).Run(); err != nil {
				return err
			}
		}
	} else {
		if err = ui.Input("AWS administrator profile", &o.Profile, false, true).Run(); err != nil {
			return err
		}
		o.Sso, o.Login = mode == "sso", mode == "login"
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "attestra-nuke-")
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
	n := &nukeWorkflow{root: root, env: env, g: g, metadata: metadata, passphrase: w.secrets.Pulumi, sharedPassphrase: w.secrets.IndexPulumi}
	var identity struct{ Account, Arn string }
	if _, err = n.api(o.Region).Call(&identity, "sts", "get-caller-identity"); err != nil {
		return err
	}
	if len(identity.Account) != 12 {
		return errors.New("invalid AWS account")
	}
	if err = os.MkdirAll(filepath.Join(root, ".attestra"), 0700); err != nil {
		return err
	}
	n.path = filepath.Join(root, ".attestra", "nuke."+identity.Account+".local.json")
	lock, err := os.OpenFile(n.path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("nuke is locked; inspect %s.lock before retrying", n.path)
	}
	fmt.Fprintf(lock, "pid %d\n", os.Getpid())
	lock.Close()
	defer os.Remove(n.path + ".lock")
	n.receipt = nukeReceipt{Repository: repo, Account: identity.Account}
	if b, e := os.ReadFile(n.path); e == nil {
		if json.Unmarshal(b, &n.receipt) != nil || n.receipt.Repository != repo || n.receipt.Account != identity.Account {
			return errors.New("nuke report identity mismatch")
		}
		if !check {
			n.receipt.Results = nil // Re-read every result; preserve inventories on retry.
			n.receipt.CheckResults, n.receipt.CheckedAt = nil, ""
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if check {
		return fmt.Errorf("no previous nuke report at %s", n.path)
	}
	if _, e := os.Stat(n.path + ".bak"); e == nil {
		return errors.New("restore interrupted nuke report from .bak before retrying")
	}
	cfToken := w.secrets.Cloudflare
	cfPrompt := "Cloudflare DNS edit token (blank records DNS as unverifiable if needed)"
	if check {
		cfPrompt = "Cloudflare DNS read token (blank records DNS as unverifiable if needed)"
	}
	if err = ui.Input(cfPrompt, &cfToken, true, false).Run(); err != nil {
		return err
	}
	if strings.TrimSpace(cfToken) != "" {
		n.cf = dns.NewClient(strings.TrimSpace(cfToken))
	}
	if check {
		return n.checkLastNuke()
	}
	n.discover()
	fmt.Printf("\nRepository: %s\nAWS account: %s\nIdentity: %s\n", repo, identity.Account, identity.Arn)
	for _, t := range n.receipt.Targets {
		fmt.Printf("DELETE %s/%s • %s • %s\n", t.Project, t.Stack, t.Backend, t.Region)
	}
	for _, b := range n.receipt.Backends {
		fmt.Printf("DELETE LAST state bucket %s (%s), only after verified cleanup\n", b.Bucket, b.Region)
	}
	fmt.Printf("Persistent report: %s\n", n.path)
	if err = typedConfirmation("Authorize permanent deletion of ALL listed environments, index/admin and data", "nuke "+repo+" "+identity.Account); err != nil {
		return err
	}
	if err = n.save(); err != nil {
		return err
	}
	if err = ensureNoNukeDeployments(g, repo); err != nil {
		return err
	}
	// Application stacks precede shared index/admin. Every target is attempted,
	// even when another target failed; recovery deletion is separately gated.
	for _, project := range []string{"attestra-auth-email", "attestra-index"} {
		for i := range n.receipt.Targets {
			if n.receipt.Targets[i].Project == project {
				n.destroyTarget(&n.receipt.Targets[i])
			}
		}
	}
	n.cleanupEnvironments(identity.Arn)
	n.record(nukeResult{"Shared OIDC provider and DNS zones", "INFO", "outside project ownership; retained because other repositories/services may use them"})
	if n.persistErr == nil {
		nukeRecovery(n.receipt.Results, func() { n.cleanupRecovery() })
	}
	if !nukeSuccessful(n.receipt.Results) || n.persistErr != nil {
		n.reportRetainedRecovery()
		n.printSummary()
		return fmt.Errorf("nuke incomplete; inspect %s and rerun to retry; confirmed ABSENT items are successful", n.path)
	}
	n.printSummary()
	fmt.Println("Nuke finished. All discovered project resources verified absent; any KMS pending-deletion dates are in the report.")
	return nil
}
