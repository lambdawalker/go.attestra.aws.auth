package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const sourceEnvironmentKey = awsenv.SourceEnvironmentKey

// Contains profile locations, never access keys, tokens, or passphrases.
type credentialSource struct {
	Profile, Region, ConfigFile, CredentialsFile, LoginCache string
	SSO                                                      bool
}
type processCredentials struct {
	Version                                    int
	AccessKeyId, SecretAccessKey, SessionToken string
	Expiration                                 time.Time
}

func parseProcessCredentials(data []byte) (processCredentials, error) {
	var c processCredentials
	if json.Unmarshal(data, &c) != nil || c.Version != 1 || c.AccessKeyId == "" || c.SecretAccessKey == "" || c.SessionToken == "" || !c.Expiration.After(time.Now()) {
		return c, errors.New("AWS profile returned missing, expired, or non-expiring credentials; sign in again with AWS CLI")
	}
	return c, nil
}
func profileSource(o options) (*credentialSource, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	file := func(key, def string) (string, error) {
		p := os.Getenv(key)
		if p == "" {
			p = def
		}
		if p == "" {
			return "", nil
		}
		// Login runs with the repository as its working directory. Preserve
		// that meaning for relative custom paths in later helper processes.
		if !filepath.IsAbs(p) && o.Root != "" {
			p = filepath.Join(o.Root, p)
		}
		return filepath.Abs(p)
	}
	c := &credentialSource{Profile: o.Profile, Region: o.Region, SSO: o.Sso}
	if c.ConfigFile, e = file("AWS_CONFIG_FILE", filepath.Join(home, ".aws", "config")); e != nil {
		return nil, e
	}
	if c.CredentialsFile, e = file("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, ".aws", "credentials")); e != nil {
		return nil, e
	}
	c.LoginCache, e = file("AWS_LOGIN_CACHE_DIRECTORY", "")
	return c, e
}
func (s credentialSource) encode() string {
	b, _ := json.Marshal(s)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCredentialSource(encoded string) (credentialSource, error) {
	var s credentialSource
	b, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil || json.Unmarshal(b, &s) != nil || strings.TrimSpace(s.Profile) == "" || s.Region == "" || !filepath.IsAbs(s.ConfigFile) || !filepath.IsAbs(s.CredentialsFile) {
		return s, errors.New("invalid AWS credential source")
	}
	return s, nil
}
func (s credentialSource) environment(base []string) []string {
	env := append(awsenv.WithoutCredentials(base), "AWS_CONFIG_FILE="+s.ConfigFile, "AWS_SHARED_CREDENTIALS_FILE="+s.CredentialsFile, "AWS_REGION="+s.Region, "AWS_DEFAULT_REGION="+s.Region, "AWS_EC2_METADATA_DISABLED=true", "AWS_PAGER=", "AWS_CLI_AUTO_PROMPT=off")
	if s.LoginCache != "" {
		env = append(env, "AWS_LOGIN_CACHE_DIRECTORY="+s.LoginCache)
	}
	return env
}
func (s credentialSource) renewalError() error {
	command := "aws login"
	if s.SSO {
		command = "aws sso login"
	}
	return fmt.Errorf("AWS credentials could not be renewed for profile %q; check access/connectivity, then run %s --profile %q and rerun the wizard to resume; existing progress is retained", s.Profile, command, s.Profile)
}
func (s credentialSource) export(ctx context.Context) (processCredentials, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "aws", "configure", "export-credentials", "--profile", s.Profile, "--format", "process")
	cmd.Env = s.environment(os.Environ())
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if cmd.Run() != nil {
		return processCredentials{}, s.renewalError()
	}
	c, e := parseProcessCredentials(out.Bytes())
	if e != nil {
		return c, s.renewalError()
	}
	return c, nil
}
func (s credentialSource) provider() aws.CredentialsProvider {
	return aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		c, e := s.export(ctx)
		if e != nil {
			return aws.Credentials{}, e
		}
		return aws.Credentials{AccessKeyID: c.AccessKeyId, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken, CanExpire: true, Expires: c.Expiration, Source: "Attestra AWS CLI profile"}, nil
	}), func(o *aws.CredentialsCacheOptions) { o.ExpiryWindow = time.Minute })
}
func prepareCloudEnvironment(base []string, c credentials, o options, empty string) ([]string, error) {
	env := cloudEnvironment(base, c, o, empty)
	if c.Source == nil {
		return env, nil
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	exe, e = credentialExecutable(exe, runtime.GOOS, shortCredentialPath)
	if e != nil {
		return nil, e
	}
	encoded := c.Source.encode()
	config := empty + ".refresh"
	content := "[profile attestra-refresh]\nregion = " + o.Region + "\ncredential_process = " + exe + " -credential-process " + encoded + "\n"
	if e = os.WriteFile(config, []byte(content), 0600); e != nil {
		return nil, e
	}
	// Explicitly remove exported keys, otherwise they override the provider chain.
	clean := []string{}
	for _, v := range env {
		k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if k == "AWS_ACCESS_KEY_ID" || k == "AWS_SECRET_ACCESS_KEY" || k == "AWS_SESSION_TOKEN" {
			continue
		}
		clean = append(clean, v)
	}
	return replaceEnvironment(clean, map[string]string{"AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": empty, "AWS_PROFILE": "attestra-refresh", "AWS_SDK_LOAD_CONFIG": "1", sourceEnvironmentKey: encoded}), nil
}
func runCredentialProcess(encoded string) error {
	source, e := decodeCredentialSource(encoded)
	if e != nil {
		return e
	}
	c, e := source.export(context.Background())
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(c)
}

// Surface only our known recovery guidance; never echo subprocess diagnostics.
func credentialRenewalFailure(env []string, stderr string) error {
	if !strings.Contains(stderr, "AWS credentials could not be renewed for profile") {
		return nil
	}
	for _, entry := range env {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, sourceEnvironmentKey) {
			source, err := decodeCredentialSource(value)
			if err == nil {
				return source.renewalError()
			}
		}
	}
	return nil
}
