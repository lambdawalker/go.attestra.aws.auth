package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type bootstrapWizard struct {
	root, temporary, passphrase, account, environment string
	o                                                 options
	r                                                 *processRunner
	a                                                 *setupAWSClient
}

func (w *bootstrapWizard) cleanup() {
	if w.temporary != "" {
		os.RemoveAll(w.temporary)
	}
}
func freshBackend() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "s3://CHOOSE-A-UNIQUE-STATE-BUCKET"
	}
	return "s3://attestra-state-" + hex.EncodeToString(b)
}
func (w *bootstrapWizard) preflight() error {
	for _, name := range []string{"go", "git", "aws", "pulumi"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("install %s and add it to PATH, then rerun setup; see docs/first-deployment.md", name)
		}
	}
	for _, name := range []string{"infra/Pulumi.yaml", "tools/deploy/go.mod", ".github/workflows/deploy.yml"} {
		if _, err := os.Stat(filepath.Join(w.root, name)); err != nil {
			return fmt.Errorf("run setup from a checkout of this repository; missing %s", name)
		}
	}
	return nil
}
func validateBootstrapApp(origin, domain, sender string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() != "" {
		return errors.New("app origin must be https://hostname, without credentials, port, path, query or fragment")
	}
	if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$`).MatchString(domain) || strings.Contains(domain, "..") {
		return errors.New("sender domain must be a DNS domain such as info.example.com")
	}
	addr, err := mail.ParseAddress(sender)
	if err != nil || addr.Address != sender || !strings.HasSuffix(strings.ToLower(sender), "@"+domain) {
		return errors.New("sender must be a plain email address in the selected sender domain")
	}
	return nil
}

// Create only on an explicit S3 not-found response. A 403 is never absence.
func ensureStateBucket(a awsSetupAPI, o options, account string, approve func(string) error) error {
	u, _ := url.Parse(o.Backend)
	_, err := a.call(nil, "s3api", "head-bucket", "--bucket", u.Host, "--expected-bucket-owner", account)
	missing := false
	if err != nil {
		var ae *awsSetupError
		if errors.As(err, &ae) && (ae.Code == "404" || ae.Code == "NoSuchBucket" || ae.Code == "NotFound") {
			missing = true
		} else {
			return err
		}
	}
	if missing {
		if err = approve("Create private state bucket " + u.Host + " in account " + account + " / " + o.Region + "? Keep its URL for resuming setup."); err != nil {
			return err
		}
		args := []string{"s3api", "create-bucket", "--bucket", u.Host}
		if o.Region != "us-east-1" {
			args = append(args, "--create-bucket-configuration", "LocationConstraint="+o.Region)
		}
		if _, err = a.call(nil, args...); err != nil {
			return err
		}
	}
	var loc struct{ LocationConstraint *string }
	if _, err = a.call(&loc, "s3api", "get-bucket-location", "--bucket", u.Host, "--expected-bucket-owner", account); err != nil {
		return err
	}
	region := "us-east-1"
	if loc.LocationConstraint != nil {
		region = *loc.LocationConstraint
		if region == "EU" {
			region = "eu-west-1"
		}
	}
	if region != o.Region {
		return fmt.Errorf("state bucket is in %s; expected %s; no bucket settings changed", region, o.Region)
	}
	if !missing {
		if err = approve("Reuse owned bucket " + u.Host + " and ensure versioning plus all public-access blocks are enabled? Existing objects and encryption are preserved."); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{"s3api", "put-public-access-block", "--bucket", u.Host, "--expected-bucket-owner", account, "--public-access-block-configuration", "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"},
		{"s3api", "put-bucket-versioning", "--bucket", u.Host, "--expected-bucket-owner", account, "--versioning-configuration", "Status=Enabled"},
	} {
		if _, err = a.call(nil, args...); err != nil {
			return err
		}
	}
	fmt.Println("✓ State bucket prepared. Existing encryption retained; new S3 buckets use encryption by default.")
	return nil
}

func (w *bootstrapWizard) prepare(a *setupAWSClient, c credentials, o options, account string) error {
	if err := validateEnvironment(w.environment); err != nil {
		return err
	}
	if o.Stack != w.environment {
		return errors.New("stack must match selected environment")
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("1 / 4 • State bucket and Pulumi stack"))
	if err := ensureStateBucket(a, o, account, confirm); err != nil {
		return err
	}
	if err := input("Stack passphrase (new stack: choose and save it; existing stack: use its original passphrase)", &c.Passphrase, true, true).Run(); err != nil {
		return err
	}
	repeated := ""
	if err := input("Repeat the stack passphrase", &repeated, true, true).Run(); err != nil {
		return err
	}
	if repeated != c.Passphrase {
		return errors.New("passphrases do not match")
	}
	base := "attestrabond.com"
	if err := input("Base domain (without https:// or an environment prefix)", &base, false, true).Validate(func(s string) error { _, e := applicationDefaults(w.environment, s); return e }).Run(); err != nil {
		return err
	}
	app, err := applicationDefaults(w.environment, base)
	if err != nil {
		return err
	}
	origin, domain, sender := app["attestra-auth-email:appOrigin"], app["attestra-auth-email:senderDomain"], app["attestra-auth-email:senderAddress"]
	fmt.Println("These application defaults fill missing configuration only. Existing configuration and proof keys are retained.")
	if err := huh.NewForm(huh.NewGroup(input("HTTPS app origin", &origin, false, true), input("SES sender domain", &domain, false, true), input("SES sender email", &sender, false, true))).Run(); err != nil {
		return err
	}
	if err := validateBootstrapApp(origin, domain, sender); err != nil {
		return err
	}
	w.temporary, err = os.MkdirTemp("", "attestra-bootstrap-")
	if err != nil {
		return err
	}
	empty := filepath.Join(w.temporary, "aws-config")
	if err = os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	w.o, w.account, w.passphrase = o, account, c.Passphrase
	env := cloudEnvironment(os.Environ(), c, o, empty)
	w.r = &processRunner{env: env}
	w.a = &setupAWSClient{env: env}
	reserved, worker := "5", "5"
	if w.environment == "dev" {
		reserved, worker = "-1", "2"
	}
	defaults := map[string]string{"aws:region": o.Region, "attestra-auth-email:appOrigin": origin, "attestra-auth-email:senderDomain": domain, "attestra-auth-email:senderAddress": sender, "attestra-auth-email:captureReservedConcurrency": reserved, "attestra-auth-email:captureWorkerMaxConcurrency": worker}
	if err = bootstrapStack(w.r, o, defaults, confirm, w.saveProofKey); err != nil {
		return err
	}
	if err = checkStack(w.r, o); err != nil {
		return err
	}
	fmt.Println("✓ Stack ready. Keep the passphrase in your password manager. It will be saved to GitHub using encrypted secret upload.")
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("2 / 4 • AWS deployment role and GitHub environment"))
	return nil
}
func (w *bootstrapWizard) saveProofKey(value string) error {
	cmd := exec.Command("pulumi", "config", "set", "attestra-auth-email:proofKey", "--secret", "--stack", w.o.Stack, "--non-interactive")
	cmd.Dir = filepath.Join(w.root, "infra")
	cmd.Env = w.r.env
	cmd.Stdin = strings.NewReader(value + "\n")
	// Never expose subprocess output for a secret write, even on failure.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.New("Pulumi secret write failed; verify passphrase/backend access; secret output suppressed")
	}
	return nil
}
func (w *bootstrapWizard) finish(repo string) error {
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("3 / 4 • Commit configuration and deploy"))
	fmt.Printf("Review and commit infra/Pulumi.%s.yaml on main, then push. This wizard does not commit or push for you:\n", w.o.Stack)
	fmt.Printf("  git diff -- infra/Pulumi.%s.yaml\n  git add infra/Pulumi.%s.yaml\n  git commit -m \"Configure %s stack\"\n  git push origin main\n", w.o.Stack, w.o.Stack, w.o.Stack)
	fmt.Printf("GitHub deployment: https://github.com/%s/actions/workflows/deploy.yml\n", repo)
	fmt.Printf("Start a NEW run on main, select environment %s: preview, then deploy. The action builds the Lambda archives. Do not retry an old commit after changing configuration.\n", w.environment)
	fmt.Println("Alternatively, you can deploy this local checkout below. Local deployment uses your current AWS session and Pulumi's confirmation prompt.")
	for {
		choice := "finish"
		if err := huh.NewSelect[string]().Title("Next step").Options(huh.NewOption("Finish here; deploy using GitHub Actions", "finish"), huh.NewOption("Build and preview locally", "preview"), huh.NewOption("Build, preview and deploy locally", "deploy"), huh.NewOption("Show SES DNS records and check verification", "dns"), huh.NewOption("Show deployed Android configuration", "outputs")).Value(&choice).Run(); err != nil {
			return err
		}
		switch choice {
		case "finish":
			fmt.Println("Setup complete. After the first deployment, rerun setup and choose the DNS check if Cognito is blocked by SES verification. See docs/first-deployment.md.")
			return nil
		case "preview", "deploy":
			o := w.o
			if choice == "preview" {
				o.CI = "preview"
			}
			if err := checkBucket(w.r, o, w.account); err != nil {
				return err
			}
			if err := execute(w.r, o); err != nil {
				fmt.Println("Deployment stopped:", err)
				fmt.Println("Existing resources/state retained. If SES was unverified, choose the DNS check; otherwise inspect the error before retrying.")
			}
		case "dns":
			if err := w.showDNS(); err != nil {
				fmt.Println("DNS check:", err)
			}
		case "outputs":
			for _, name := range []string{"apiUrl", "userPoolId", "clientId"} {
				if _, err := w.r.Exec(filepath.Join(w.root, "infra"), false, "pulumi", "stack", "output", name, "--stack", w.o.Stack); err != nil {
					fmt.Println("Outputs unavailable; finish deployment first.")
					break
				}
			}
			fmt.Println("Apply these outputs to Android; see README.md → Configure the Android API URL. Real ID capture remains disabled until deliberately configured.")
		}
	}
}
func (w *bootstrapWizard) showDNS() error {
	fmt.Println(lipgloss.NewStyle().Bold(true).Render("4 / 4 • SES verification (Cloudflare DNS is manual)"))
	data, err := w.r.Exec(filepath.Join(w.root, "infra"), true, "pulumi", "config", "get", "attestra-auth-email:senderDomain", "--stack", w.o.Stack)
	if err != nil {
		return err
	}
	domain := strings.TrimSpace(string(data))
	var verification struct {
		VerificationAttributes map[string]struct{ VerificationStatus, VerificationToken string }
	}
	var dkim struct {
		DkimAttributes map[string]struct {
			DkimVerificationStatus string
			DkimTokens             []string
		}
	}
	if _, err = w.a.call(&verification, "ses", "get-identity-verification-attributes", "--identities", domain); err != nil {
		return err
	}
	if _, err = w.a.call(&dkim, "ses", "get-identity-dkim-attributes", "--identities", domain); err != nil {
		return err
	}
	v, exists := verification.VerificationAttributes[domain]
	if !exists {
		return errors.New("SES identity does not exist in this account/region yet; deploy first, then check again")
	}
	d := dkim.DkimAttributes[domain]
	fmt.Printf("Account %s • Region %s • Identity %s\nVerification: %s • DKIM: %s\n", w.account, w.o.Region, domain, v.VerificationStatus, d.DkimVerificationStatus)
	if v.VerificationToken != "" {
		fmt.Printf("TXT    _amazonses.%s    %s\n", domain, v.VerificationToken)
	}
	for _, token := range d.DkimTokens {
		fmt.Printf("CNAME  %s._domainkey.%s    %s.dkim.amazonses.com\n", token, domain, token)
	}
	fmt.Println("Copy these full names/values into the authoritative DNS zone. Use DNS only for CNAMEs; preserve unrelated website/R2/mail records. Compare old values before replacing them.")
	if v.VerificationStatus == "Success" && d.DkimVerificationStatus == "Success" {
		fmt.Println("✓ SES verified. Resume deployment if Cognito previously failed.")
	} else {
		fmt.Println("Verification pending. Publish the records, allow DNS propagation, then select this check again. Do not delete the stack.")
	}
	fmt.Println("A verified sender subdomain is sufficient; a separate unverified root identity is not a blocker. SES sandbox recipient restrictions still apply.")
	return nil
}
