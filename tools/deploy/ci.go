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
	return execute(r, o)
}
