package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// Let aws login use normal profile/cache files, while removing ambient credentials
// and endpoint overrides. Deployment commands later use only the exported values.
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
	if _, err := r.Exec(o.Root, false, "aws", "login", "--profile", o.Profile, "--region", o.Region); err != nil {
		return credentials{}, errors.New("AWS login failed; use a current AWS CLI v2 with aws login support")
	}
	// Capture JSON in memory, never stream credentials to the terminal or a file.
	data, err := r.Exec(o.Root, true, "aws", "configure", "export-credentials", "--profile", o.Profile, "--format", "process")
	if err != nil {
		return credentials{}, errors.New("could not obtain credentials from the AWS login profile")
	}
	var result struct {
		Version                                    int
		AccessKeyId, SecretAccessKey, SessionToken string
	}
	if json.Unmarshal(data, &result) != nil || result.Version != 1 || result.AccessKeyId == "" || result.SecretAccessKey == "" || result.SessionToken == "" {
		return credentials{}, errors.New("AWS login returned incomplete temporary credentials")
	}
	return credentials{Access: result.AccessKeyId, Secret: result.SecretAccessKey, Token: result.SessionToken}, nil
}
