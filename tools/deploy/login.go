package main

import (
	"errors"
	"strings"
)

// Let aws login use normal profile/cache files, while removing ambient credentials
// and endpoint overrides. Deployment commands later use a refreshable process provider.
func loginEnvironment(base []string, region string) []string {
	env := withoutCredentials(base)
	for _, entry := range base {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		switch key {
		case "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_LOGIN_CACHE_DIRECTORY":
			env = append(env, entry)
		}
	}
	return append(env, "AWS_REGION="+region, "AWS_DEFAULT_REGION="+region, "AWS_PAGER=", "AWS_CLI_AUTO_PROMPT=off")
}
func loginCredentials(r commandRunner, o options) (credentials, error) {
	if strings.TrimSpace(o.Profile) == "" {
		return credentials{}, errors.New("AWS login profile is required")
	}
	args := []string{"login", "--profile", o.Profile, "--region", o.Region}
	failure := "AWS console login failed; see the AWS error above. For an IAM Identity Center profile, use -Sso; aws login requires AWS CLI v2.32.0+"
	if o.Sso {
		// The SSO region comes from the profile/session and may differ from the
		// application's deployment region. Do not override it with --region.
		args = []string{"sso", "login", "--profile", o.Profile}
		failure = "AWS SSO login failed; see the AWS error above and verify this profile's IAM Identity Center configuration"
	}
	if _, err := r.Exec(o.Root, false, "aws", args...); err != nil {
		return credentials{}, errors.New(failure)
	}
	// Capture JSON in memory, never stream credentials to the terminal or a file.
	data, err := r.Exec(o.Root, true, "aws", "configure", "export-credentials", "--profile", o.Profile, "--format", "process")
	if err != nil {
		return credentials{}, errors.New("could not obtain credentials from the AWS login profile")
	}
	if _, err := parseProcessCredentials(data); err != nil {
		return credentials{}, err
	}
	source, err := profileSource(o)
	if err != nil {
		return credentials{}, err
	}
	return credentials{Source: source}, nil
}
