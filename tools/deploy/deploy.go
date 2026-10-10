package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type options struct {
	ReuseBuild                                             bool // Internal: only after a successful build in this staged run.
	Targets                                                []string
	Root, Stack, Backend, Region, MigrateFrom, Profile, CI string
	Pull, Login, Sso                                       bool
}
type credentials struct{ Access, Secret, Token, Passphrase, CloudToken string }
type commandRunner interface {
	Exec(dir string, capture bool, name string, args ...string) ([]byte, error)
}
type processRunner struct{ env []string }

func (p *processRunner) Exec(dir string, capture bool, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = withoutCredentials(os.Environ())
	if (name == "aws" || name == "pulumi") && p.env != nil {
		cmd.Env = p.env
	}
	if name == "pulumi" && len(args) > 0 && (args[0] == "preview" || args[0] == "up") {
		stack := ""
		for i := 1; i+1 < len(args); i++ {
			if args[i] == "--stack" {
				stack = args[i+1]
				break
			}
		}
		if stack == "" {
			return nil, errors.New("explicit stack required for Lambda quota check")
		}
		effective, err := captureConcurrencyPreflight(p, dir, stack)
		if err != nil {
			return nil, err
		}
		cmd.Env = captureEnvironment(cmd.Env, effective)
	}
	cmd.Stdin = os.Stdin
	var out bytes.Buffer
	if capture {
		cmd.Stdout = &out
		cmd.Stderr = &bytes.Buffer{}
	} else {
		fmt.Printf("\n→ %s %s\n", name, strings.Join(args, " "))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return out.Bytes(), nil
}
func withoutCredentials(base []string) []string {
	result := []string{}
	for _, entry := range base {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(key, "AWS_") || strings.HasPrefix(key, "PULUMI_") {
			continue
		}
		// Do not let the developer's cross-compilation flags affect host build tools.
		if key == "GOOS" || key == "GOARCH" || key == "CGO_ENABLED" {
			continue
		}
		result = append(result, entry)
	}
	return result
}
func cloudEnvironment(base []string, c credentials, o options, empty string) []string {
	result := append(withoutCredentials(base),
		"AWS_ACCESS_KEY_ID="+c.Access, "AWS_SECRET_ACCESS_KEY="+c.Secret,
		"AWS_SESSION_TOKEN="+c.Token, "AWS_REGION="+o.Region, "AWS_DEFAULT_REGION="+o.Region,
		"AWS_CONFIG_FILE="+empty, "AWS_SHARED_CREDENTIALS_FILE="+empty,
		"AWS_EC2_METADATA_DISABLED=true", "AWS_PAGER=", "AWS_CLI_AUTO_PROMPT=off",
		"PULUMI_CONFIG_PASSPHRASE="+c.Passphrase,
		"PULUMI_BACKEND_URL="+o.backendURL())
	if o.MigrateFrom != "" && c.CloudToken != "" {
		result = append(result, "PULUMI_ACCESS_TOKEN="+c.CloudToken)
	}
	return result
}
func (o options) validate() error {
	u, err := url.Parse(o.Backend)
	if err != nil || u.Scheme != "s3" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return errors.New("state URL must be s3://bucket or s3://bucket/prefix, without credentials or query parameters")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(u.Host) {
		return errors.New("invalid S3 bucket name")
	}
	if strings.Contains(u.Path, "..") || strings.ContainsAny(u.Path, "\r\n\\") {
		return errors.New("invalid S3 state prefix")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(o.Stack) {
		return errors.New("use a simple stack name, e.g. dev")
	}
	if !regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`).MatchString(o.Region) {
		return errors.New("invalid AWS region")
	}
	if o.MigrateFrom != "" {
		parts := strings.Split(o.MigrateFrom, "/")
		if len(parts) != 3 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`).MatchString(parts[0]) || parts[1] != "attestra-auth-email" || parts[2] != o.Stack {
			return errors.New("migration source must be OWNER/attestra-auth-email/STACK, with the same stack name")
		}
		if o.Pull {
			return errors.New("--pull cannot be combined with migration")
		}
	}
	return nil
}
func (o options) backendURL() string {
	u, _ := url.Parse(o.Backend)
	q := url.Values{"region": {o.Region}, "awssdk": {"v2"}}
	u.RawQuery = q.Encode()
	return u.String()
}
func checkBucket(r commandRunner, o options, account string) error {
	u, _ := url.Parse(o.Backend)
	_, err := r.Exec(o.Root, true, "aws", "s3api", "head-bucket", "--bucket", u.Host, "--expected-bucket-owner", account)
	if err != nil {
		return errors.New("state bucket is unavailable or belongs to another account; create a private, versioned bucket in this account first (see docs/deployment.md)")
	}
	data, err := r.Exec(o.Root, true, "aws", "s3api", "get-bucket-versioning", "--bucket", u.Host, "--expected-bucket-owner", account, "--output", "json")
	if err != nil {
		return errors.New("could not check state bucket versioning")
	}
	var version struct{ Status string }
	if json.Unmarshal(data, &version) != nil || version.Status != "Enabled" {
		return errors.New("enable versioning on the state bucket before continuing")
	}
	data, err = r.Exec(o.Root, true, "aws", "s3api", "get-public-access-block", "--bucket", u.Host, "--expected-bucket-owner", account, "--output", "json")
	if err != nil {
		return errors.New("could not check the state bucket's public access block")
	}
	var privacy struct {
		PublicAccessBlockConfiguration struct{ BlockPublicAcls, IgnorePublicAcls, BlockPublicPolicy, RestrictPublicBuckets bool }
	}
	if json.Unmarshal(data, &privacy) != nil {
		return errors.New("invalid bucket public access configuration")
	}
	b := privacy.PublicAccessBlockConfiguration
	if !b.BlockPublicAcls || !b.IgnorePublicAcls || !b.BlockPublicPolicy || !b.RestrictPublicBuckets {
		return errors.New("enable all four public access block settings on the state bucket")
	}
	return nil
}
func pullRepository(r commandRunner, root string) error {
	status, err := r.Exec(root, true, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return errors.New("commit or stash changes before using --pull")
	}
	_, err = r.Exec(root, false, "git", "pull", "--ff-only")
	return err
}
func execute(r commandRunner, o options) error {
	infra := filepath.Join(o.Root, "infra")
	if o.MigrateFrom != "" {
		// Migration creates a destination stack only through the dedicated migrate
		// command. Never initialize an empty stack after an arbitrary select failure.
		version, err := r.Exec(infra, true, "pulumi", "version")
		var major, minor, patch int
		_, parseErr := fmt.Sscanf(strings.TrimSpace(string(version)), "v%d.%d.%d", &major, &minor, &patch)
		if err != nil || parseErr != nil || major < 3 || (major == 3 && minor < 254) {
			return errors.New("migration needs Pulumi CLI 3.254.0 or later")
		}
		if _, err := r.Exec(infra, false, "pulumi", "stack", "migrate", "https://api.pulumi.com", o.MigrateFrom, "--target", o.Stack, "--secrets-provider", "passphrase"); err != nil {
			return fmt.Errorf("migration did not complete; inspect the destination and local configuration before retrying; nothing deployed: %w", err)
		}
		if _, err := r.Exec(infra, false, "pulumi", "config", "rm", "aws:profile", "--stack", o.Stack); err != nil {
			return fmt.Errorf("state migrated, but profile removal failed; nothing deployed: %w", err)
		}
		if err := checkStack(r, o); err != nil {
			return fmt.Errorf("state migrated; review required before deployment: %w", err)
		}
		fmt.Println("\nMigration finished; no infrastructure deployed. Review and commit infra/Pulumi." + o.Stack + ".yaml. Keep the encrypted .bak backup privately. Use only the S3 backend for future deployments; the source stack was retained.")
		return nil
	}
	if _, err := r.Exec(infra, true, "pulumi", "stack", "select", o.Stack, "--non-interactive"); err != nil {
		return errors.New("cannot select the S3 stack; check access or run --migrate-from OWNER/attestra-auth-email/STACK once; no empty stack was created")
	}
	if err := checkStack(r, o); err != nil {
		return err
	}
	if o.ReuseBuild {
		if err := checkLambdaArchives(o.Root); err != nil {
			return err
		}
		fmt.Println("Reusing Lambda archives built earlier in this setup run.")
	} else {
		name, args := buildCommand("")
		if _, err := r.Exec(o.Root, false, name, args...); err != nil {
			return err
		}
	}
	previewArgs := []string{"preview", "--stack", o.Stack}
	for _, target := range o.Targets {
		previewArgs = append(previewArgs, "--target", target)
	}
	if o.CI != "" {
		previewArgs = append(previewArgs, "--non-interactive")
	}
	if _, err := r.Exec(infra, false, "pulumi", previewArgs...); err != nil {
		return err
	}
	if o.CI == "preview" {
		return nil
	}
	// Interactive runs retain confirmation. CI requires explicit deploy mode.
	upArgs := []string{"up", "--stack", o.Stack}
	for _, target := range o.Targets {
		upArgs = append(upArgs, "--target", target)
	}
	if o.CI == "deploy" {
		upArgs = append(upArgs, "--yes", "--non-interactive")
	}
	if _, err := r.Exec(infra, false, "pulumi", upArgs...); err != nil {
		return err
	}
	fmt.Println("\nDeployment finished.")
	return nil
}
func buildCommand(_ string) (string, []string) {
	return "go", []string{"-C", "tools/deploy", "run", ".", "-build"}
}
func checkStack(r commandRunner, o options) error {
	infra := filepath.Join(o.Root, "infra")
	data, err := r.Exec(infra, true, "pulumi", "stack", "export", "--stack", o.Stack)
	if err != nil {
		return errors.New("could not read S3 stack state; no deployment started")
	}
	var state struct {
		Deployment struct {
			SecretsProviders struct{ Type string } `json:"secrets_providers"`
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment.SecretsProviders.Type != "passphrase" {
		return errors.New("S3 stack must use passphrase secrets encryption; migrate it with --secrets-provider passphrase first")
	}
	data, err = r.Exec(infra, true, "pulumi", "config", "--json", "--stack", o.Stack)
	if err != nil {
		return errors.New("could not read stack configuration")
	}
	var config map[string]struct{ Value string }
	if err := json.Unmarshal(data, &config); err != nil {
		return errors.New("invalid stack configuration")
	}
	for _, key := range []string{"profile", "accessKey", "secretKey", "token", "assumeRole", "assumeRoles", "assumeRoleWithWebIdentity", "sharedCredentialsFiles", "sharedConfigFiles", "endpoints"} {
		if _, exists := config["aws:"+key]; exists {
			return fmt.Errorf("remove aws:%s from this stack's configuration so the entered credentials are used", key)
		}
	}
	if config["aws:region"].Value != o.Region {
		return errors.New("entered AWS region must match the stack's aws:region configuration")
	}
	return nil
}
