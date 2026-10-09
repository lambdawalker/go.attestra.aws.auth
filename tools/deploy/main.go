// deploy is an interactive local deployment tool. AWS CLI manages login caching.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

func required(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("Required")
	}
	return nil
}
func input(title string, value *string, secret, mandatory bool) *huh.Input {
	field := huh.NewInput().Title(title).Value(value)
	if secret {
		field.EchoMode(huh.EchoModePassword)
	}
	if mandatory {
		field.Validate(required)
	}
	return field
}
func confirm(title string) error {
	yes := false
	if err := huh.NewConfirm().Title(title).Affirmative("Continue").Negative("Cancel").Value(&yes).Run(); err != nil {
		return err
	}
	if !yes {
		return errors.New("cancelled")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Deployment stopped:", err)
		os.Exit(1)
	}
}
func run() error {
	var o options
	var setupGitHub bool
	flag.StringVar(&o.Root, "repo-root", "../..", "Repository root (default: run from tools/deploy)")
	flag.StringVar(&o.Stack, "stack", "dev", "Existing stack name")
	flag.StringVar(&o.Backend, "backend", "", "S3 state URL, e.g. s3://my-state-bucket")
	flag.StringVar(&o.Region, "region", "us-east-2", "AWS region")
	flag.StringVar(&o.MigrateFrom, "migrate-from", "", "One-time migration from a fully qualified Pulumi Cloud stack; stops before deployment")
	flag.BoolVar(&o.Pull, "pull", false, "Require a clean working tree and git pull --ff-only before deployment")
	flag.BoolVar(&o.Login, "login", false, "Use aws login instead of prompting for AWS credentials")
	flag.BoolVar(&o.Sso, "sso", false, "Use aws sso login with an existing IAM Identity Center profile")
	flag.StringVar(&o.Profile, "profile", "default", "AWS CLI profile used with -login or -sso")
	flag.StringVar(&o.CI, "ci", "", "Noninteractive mode: preview or deploy; reads AWS credentials and Pulumi passphrase from environment")
	flag.BoolVar(&setupGitHub, "setup-github", false, "Create or configure the GitHub dev environment interactively")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if setupGitHub {
		if o.CI != "" {
			return errors.New("GitHub setup cannot run in CI mode")
		}
		return runGitHubSetup()
	}
	if o.CI != "" {
		return runCI(o)
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("run in an interactive terminal; credentials are entered using hidden prompts")
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	o.Root = root
	for _, file := range []string{"infra/Pulumi.yaml", "build.ps1", "build.sh"} {
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
		input("S3 state URL (bucket must already exist)", &o.Backend, false, true),
		input("AWS region", &o.Region, false, true),
		input("Stack", &o.Stack, false, true),
	)).Run(); err != nil {
		return err
	}
	if err := o.validate(); err != nil {
		return err
	}
	c := credentials{}
	if o.Login || o.Sso {
		if err := input("AWS login profile", &o.Profile, false, true).Run(); err != nil {
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
			input("AWS access key ID", &c.Access, true, true),
			input("AWS secret access key", &c.Secret, true, true),
			input("AWS session token (required for temporary credentials)", &c.Token, true, false),
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
	if err := input("Pulumi state passphrase (keep a recoverable copy)", &c.Passphrase, true, true).Run(); err != nil {
		return err
	}
	if o.MigrateFrom != "" {
		var repeated string
		if err := input("Repeat the new state passphrase", &repeated, true, true).Run(); err != nil {
			return err
		}
		if repeated != c.Passphrase {
			return errors.New("passphrases do not match")
		}
		if err := input("Pulumi Cloud token for migration only (blank uses your saved Pulumi login)", &c.CloudToken, true, false).Run(); err != nil {
			return err
		}
	}
	// Empty AWS files prevent credential_process, local profiles and role chaining
	// from silently replacing the credentials supplied above.
	dir, err := os.MkdirTemp("", "attestra-deploy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	empty := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	env := cloudEnvironment(os.Environ(), c, o, empty)
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
	if err := confirm("Use this AWS identity and state location?"); err != nil {
		return err
	}
	if err := checkBucket(runner, o, id.Account); err != nil {
		return err
	}
	if o.MigrateFrom != "" {
		if err := confirm("Migrate " + o.MigrateFrom + " to S3 and rewrite the local stack configuration?"); err != nil {
			return err
		}
	} else if o.Pull {
		// Git runs without the prompted secrets.
		if err := pullRepository(&processRunner{}, o.Root); err != nil {
			return err
		}
	}
	return execute(runner, o)
}
