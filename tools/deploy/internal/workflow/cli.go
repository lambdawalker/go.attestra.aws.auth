// deploy is an interactive local deployment tool. AWS CLI manages login caching.
package workflow

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	lambdabuild "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/build"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

func Run() error {
	if len(os.Args) == 3 && os.Args[1] == "-credential-process" {
		return runCredentialProcess(os.Args[2])
	}
	var o options
	var setupGitHub, bootstrap, build, teardown, forceSetup, freshCredentials, manageVault, teardownIndex bool
	flag.StringVar(&o.ExportAndroid, "export-android", "", "Export deployed public Android settings to a properties file without deployment")
	flag.BoolVar(&manageVault, "manage-credentials", false, "Manage the local credential vault without running setup")
	flag.BoolVar(&o.HealthCheck, "health-check", false, "Check the deployed environment without building or deploying")
	flag.StringVar(&o.Root, "repo-root", "../..", "Repository root (default: run from tools/deploy)")
	flag.StringVar(&o.Stack, "stack", "dev", "Existing stack name")
	flag.StringVar(&o.Backend, "backend", "", "S3 state URL, e.g. s3://my-state-bucket")
	flag.StringVar(&o.Region, "region", "us-east-2", "AWS region")
	flag.StringVar(&o.MigrateFrom, "migrate-from", "", "One-time migration from a fully qualified Pulumi Cloud stack; stops before deployment")
	flag.BoolVar(&o.ReleaseIndex, "release-index-lock", false, "Release an interrupted deployment using its saved receipt, after inspection")
	flag.BoolVar(&o.PublishOnly, "publish-index", false, "Retry a pending index publication without redeploying")
	flag.BoolVar(&o.Pull, "pull", false, "Require a clean working tree and git pull --ff-only before deployment")
	flag.BoolVar(&o.Login, "login", false, "Use aws login instead of prompting for AWS credentials")
	flag.BoolVar(&o.Sso, "sso", false, "Use aws sso login with an existing IAM Identity Center profile")
	flag.StringVar(&o.Profile, "profile", "default", "AWS CLI profile used with -login or -sso")
	flag.StringVar(&o.CI, "ci", "", "Noninteractive mode: preview, deploy, or publish; reads AWS credentials and Pulumi passphrase from environment")
	flag.BoolVar(&setupGitHub, "setup-github", false, "Create or configure the selected GitHub environment interactively")
	flag.BoolVar(&freshCredentials, "fresh-credentials", false, "Enter new credentials instead of unlocking the local vault (requires -bootstrap)")
	flag.BoolVar(&forceSetup, "force-setup", false, "Bypass the local completed-setup check and reconcile remote services (requires -bootstrap)")
	flag.BoolVar(&bootstrap, "bootstrap", false, "Guide a fresh environment deployment, or resume setup")
	flag.BoolVar(&teardownIndex, "teardown-index", false, "Interactively remove the shared index after all environments are torn down")
	flag.BoolVar(&teardown, "teardown", false, "Interactively tear down an environment and its GitHub/Cloudflare configuration")
	flag.BoolVar(&build, "build", false, "Build all Linux ARM64 Lambda ZIP archives")
	if err := flag.CommandLine.Parse(normalizeArguments(os.Args[1:])); err != nil {
		return err
	}
	if o.ExportAndroid != "" && (o.HealthCheck || build || bootstrap || setupGitHub || teardown || o.CI != "" || o.MigrateFrom != "" || o.Pull || o.PublishOnly || o.ReleaseIndex) {
		return errors.New("-export-android cannot be combined with other operation flags")
	}
	if o.HealthCheck && (build || bootstrap || setupGitHub || teardown || o.CI != "" || o.MigrateFrom != "" || o.Pull || o.PublishOnly || o.ReleaseIndex) {
		return errors.New("-health-check cannot be combined with deployment, setup, build, migration, index recovery, or CI operation flags")
	}
	if (o.PublishOnly || o.ReleaseIndex) && (build || bootstrap || setupGitHub || teardown || o.MigrateFrom != "" || o.Pull || (o.PublishOnly && o.ReleaseIndex) || o.CI != "") {
		return errors.New("index recovery flags cannot be combined with other operation flags")
	}
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	if !build {
		if err := prepareGeneratedState(root); err != nil {
			return err
		}
	}
	if manageVault {
		unsupported := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "manage-credentials" && f.Name != "repo-root" {
				unsupported = f.Name
			}
		})
		if unsupported != "" {
			return fmt.Errorf("-%s cannot be combined with -manage-credentials", unsupported)
		}
		return manageCredentials(root)
	}

	if (forceSetup || freshCredentials) && !bootstrap {
		return errors.New("-force-setup and -fresh-credentials require -bootstrap")
	}
	if teardownIndex {
		var unsupported string
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "teardown-index" && f.Name != "repo-root" {
				unsupported = f.Name
			}
		})
		if unsupported != "" {
			return fmt.Errorf("-%s cannot be combined with -teardown-index", unsupported)
		}
		return runIndexTeardown(root)
	}
	if teardown {
		var unsupported string
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "teardown" && f.Name != "repo-root" {
				unsupported = f.Name
			}
		})
		if unsupported != "" {
			return fmt.Errorf("-%s is not supported with teardown; select the target in its reviewed interactive flow", unsupported)
		}
		if build || bootstrap || setupGitHub || o.CI != "" || o.MigrateFrom != "" || o.Pull {
			return errors.New("-teardown cannot be combined with build, setup, migration, pull, or CI")
		}
		return runTeardown(root)
	}
	if build {
		if bootstrap || setupGitHub || o.CI != "" {
			return errors.New("-build cannot be combined with setup or CI modes")
		}
		return lambdabuild.Lambdas(root)
	}
	if setupGitHub || bootstrap {
		if o.CI != "" {
			return errors.New("GitHub setup cannot run in CI mode")
		}
		return runGitHubSetup(root, bootstrap, forceSetup || freshCredentials, freshCredentials)
	}
	if o.CI != "" {
		return runCI(o)
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("run in an interactive terminal; credentials are entered using hidden prompts")
	}
	o.Root = root
	for _, file := range []string{"infra/Pulumi.yaml", "build.bat", "build.sh"} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			return fmt.Errorf("invalid repository root: missing %s", file)
		}
	}
	for _, name := range []string{"aws", "go", "pulumi"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("install %s and add it to PATH", name)
		}
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).Render("Attestra • Deploy"))
	fmt.Println("S3 state • interactive AWS authentication")
	if err := huh.NewForm(huh.NewGroup(
		ui.Input("S3 state URL (bucket must already exist)", &o.Backend, false, true),
		ui.Input("AWS region", &o.Region, false, true),
		ui.Input("Stack", &o.Stack, false, true),
	)).Run(); err != nil {
		return err
	}
	if err := o.validate(); err != nil {
		return err
	}
	c := credentials{}
	if o.Login || o.Sso {
		if err := ui.Input("AWS login profile", &o.Profile, false, true).Run(); err != nil {
			return err
		}
		fmt.Println("AWS CLI will open its login flow and cache the session in your selected profile.")
		loginRunner := &processRunner{env: loginEnvironment(os.Environ(), o.Region)}
		c, err = loginCredentials(loginRunner, o)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Temporary AWS credentials need all three fields. For long-lived keys only, leave session token blank.")
		if err := huh.NewForm(huh.NewGroup(
			ui.Input("AWS access key ID", &c.Access, true, true),
			ui.Input("AWS secret access key", &c.Secret, true, true),
			ui.Input("AWS session token (required for temporary credentials)", &c.Token, true, false),
		)).Run(); err != nil {
			return err
		}
		c.Access = strings.TrimSpace(c.Access)
		c.Secret = strings.TrimSpace(c.Secret)
		c.Token = strings.TrimSpace(c.Token)
		if strings.HasPrefix(c.Access, "ASIA") && c.Token == "" {
			return errors.New("temporary AWS access keys require the session token")
		}
	}
	if err := ui.Input("Pulumi state passphrase (keep a recoverable copy)", &c.Passphrase, true, true).Run(); err != nil {
		return err
	}
	if o.MigrateFrom != "" {
		var repeated string
		if err := ui.Input("Repeat the new state passphrase", &repeated, true, true).Run(); err != nil {
			return err
		}
		if repeated != c.Passphrase {
			return errors.New("passphrases do not match")
		}
		if err := ui.Input("Pulumi Cloud token for migration only (blank uses your saved Pulumi login)", &c.CloudToken, true, false).Run(); err != nil {
			return err
		}
	}
	// Isolate pasted keys from ambient profiles. Profile login instead installs
	// an explicit, refreshable provider in a separate temporary configuration.
	dir, err := os.MkdirTemp("", "attestra-deploy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	empty := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env, err := prepareCloudEnvironment(os.Environ(), c, o, empty)
	if err != nil {
		return err
	}
	runner := &processRunner{env: env}
	identity, err := runner.Exec(o.Root, true, "aws", "sts", "get-caller-identity", "--output", "json", "--no-cli-pager")
	if err != nil {
		return errors.New("AWS credentials could not be verified; check all credential fields and their expiry")
	}
	var id struct {
		Account string
		Arn     string
	}
	if err := json.Unmarshal(identity, &id); err != nil || id.Account == "" {
		return errors.New("invalid AWS identity response")
	}
	fmt.Printf("Account: %s\nIdentity: %s\nRegion: %s\nState: %s\nStack: %s\n", id.Account, id.Arn, o.Region, o.Backend, o.Stack)
	if err := ui.Confirm("Use this AWS identity and state location?"); err != nil {
		return err
	}
	if err := checkBucket(runner, o, id.Account); err != nil {
		return err
	}
	if o.MigrateFrom != "" {
		if err := ui.Confirm("Migrate " + o.MigrateFrom + " to S3 and rewrite the local stack configuration?"); err != nil {
			return err
		}
	} else if o.Pull {
		// Git runs without the prompted secrets.
		if err := pullRepository(&processRunner{}, o.Root); err != nil {
			return err
		}
	}
	if o.ExportAndroid != "" {
		return exportAndroidConfiguration(runner, o, o.ExportAndroid)
	}
	if o.HealthCheck {
		return runEnvironmentHealth(runner, o)
	}
	if err := execute(runner, o); err != nil {
		return err
	}
	if o.MigrateFrom == "" && !o.ReleaseIndex {
		return runEnvironmentHealth(runner, o)
	}
	return nil
}

// Retain the old Windows launcher spellings while all launchers now invoke Go.
func normalizeArguments(args []string) []string {
	result := append([]string(nil), args...)
	names := map[string]string{"stack": "stack", "backend": "backend", "region": "region", "migratefrom": "migrate-from", "pull": "pull", "login": "login", "sso": "sso", "profile": "profile"}
	for i, arg := range result {
		if strings.HasPrefix(arg, "-") {
			parts := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)
			if name, ok := names[strings.ToLower(parts[0])]; ok {
				result[i] = "-" + name
				if len(parts) == 2 {
					result[i] += "=" + parts[1]
				}
			}
		}
	}
	return result
}
