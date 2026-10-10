package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func validateCI(o options, c credentials) error {
	if o.CI != "preview" && o.CI != "deploy" {
		return errors.New("CI mode must be preview or deploy")
	}
	if o.MigrateFrom != "" || o.Login || o.Sso || o.Pull {
		return errors.New("CI cannot migrate, log in interactively, or pull Git changes")
	}
	if c.Access == "" || c.Secret == "" || c.Token == "" || c.Passphrase == "" {
		return errors.New("CI requires temporary AWS credentials and PULUMI_CONFIG_PASSPHRASE; configure AWS OIDC and the GitHub environment secret")
	}
	return o.validate()
}
func runCI(o options) error {
	c := credentials{Access: os.Getenv("AWS_ACCESS_KEY_ID"), Secret: os.Getenv("AWS_SECRET_ACCESS_KEY"), Token: os.Getenv("AWS_SESSION_TOKEN"), Passphrase: os.Getenv("PULUMI_CONFIG_PASSPHRASE")}
	if err := validateCI(o, c); err != nil {
		return err
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	o.Root = root
	if _, err := os.Stat(filepath.Join(root, "infra", "Pulumi.yaml")); err != nil {
		return errors.New("invalid repository root")
	}
	dir, err := os.MkdirTemp("", "attestra-ci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	empty := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		return err
	}
	r := &processRunner{env: cloudEnvironment(os.Environ(), c, o, empty)}
	data, err := r.Exec(root, true, "aws", "sts", "get-caller-identity", "--output", "json", "--no-cli-pager")
	if err != nil {
		return err
	}
	var identity struct{ Account string }
	if json.Unmarshal(data, &identity) != nil || identity.Account == "" {
		return errors.New("invalid AWS identity response")
	}
	fmt.Printf("CI %s • account %s • stack %s • backend %s\n", o.CI, identity.Account, o.Stack, o.Backend)
	if err := checkBucket(r, o, identity.Account); err != nil {
		return err
	}
	if o.CI == "deploy" {
		wizard := &bootstrapWizard{root: root, o: o, r: r, a: &setupAWSClient{env: r.env}}
		configData, e := r.Exec(filepath.Join(root, "infra"), true, "pulumi", "config", "--json", "--stack", o.Stack)
		if e != nil {
			return e
		}
		var config map[string]struct{ Value string }
		if json.Unmarshal(configData, &config) != nil {
			return errors.New("invalid stack configuration")
		}
		if domain := config["attestra-auth-email:apiDomain"].Value; domain != "" {
			arn, e := wizard.output("apiCertificateArn")
			if e != nil {
				return errors.New("API certificate prerequisites missing; run the setup wizard first")
			}
			cert, e := wizard.certificateStatus(arn)
			if e != nil {
				return e
			}
			if cert.Certificate.DomainName != domain || cert.Certificate.Status != "ISSUED" {
				return errors.New("API certificate is not issued for configured apiDomain; run setup to validate DNS before deploying")
			}
		}
		evidence, err := wizard.readSES()
		if err != nil {
			return fmt.Errorf("SES readiness check failed; run setup and choose Build, preview and deploy to complete SES/DNS prerequisites: %w", err)
		}
		if !sesReady(evidence) {
			return fmt.Errorf("SES %s is not ready (verification %s, DKIM %s); run setup and choose Build, preview and deploy to configure DNS and wait for verification; no deployment attempted", evidence.Domain, evidence.Verification, evidence.DKIM)
		}
	}
	return execute(r, o)
}
