package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessCredentialsRequireFutureExpiration(t *testing.T) {
	for _, expiry := range []string{"", "2000-01-01T00:00:00Z"} {
		data, _ := json.Marshal(map[string]any{"Version": 1, "AccessKeyId": "key", "SecretAccessKey": "secret", "SessionToken": "token", "Expiration": expiry})
		if _, err := parseProcessCredentials(data); err == nil {
			t.Fatal("accepted unusable credentials")
		}
	}
}

func TestRefreshEnvironmentIsolatedAndOriginalProfilePreserved(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, []byte("original profile"), 0600); err != nil {
		t.Fatal(err)
	}
	source := &credentialSource{Profile: "attestra", Region: "us-east-2", ConfigFile: original, CredentialsFile: filepath.Join(dir, "credentials"), SSO: true}
	env, err := prepareCloudEnvironment([]string{"AWS_ACCESS_KEY_ID=ambient", "AWS_ENDPOINT_URL=https://wrong", "AWS_PROFILE=wrong"}, credentials{Source: source, Passphrase: "private-passphrase"}, options{Region: "us-east-2"}, filepath.Join(dir, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "AWS_ACCESS_KEY_ID=") || strings.HasPrefix(entry, "AWS_ENDPOINT_URL=") {
			t.Fatalf("ambient credentials survived: %s", entry)
		}
	}
	config, err := os.ReadFile(filepath.Join(dir, "empty.refresh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "credential_process =") || strings.Contains(string(config), "private-passphrase") {
		t.Fatal("invalid process profile")
	}
	contents, _ := os.ReadFile(original)
	if string(contents) != "original profile" {
		t.Fatal("original profile modified")
	}
	restored := strings.Join(source.environment(env), "\n")
	if !strings.Contains(restored, "AWS_CONFIG_FILE="+original) || strings.Contains(restored, "attestra-refresh") || strings.Contains(restored, sourceEnvironmentKey+"=") {
		t.Fatal("source environment recurses into helper")
	}
}

func TestProfileProviderRenewsAndRedactsFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI uses a POSIX shell")
	}
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	t.Setenv("ATTESTRA_TEST_PAYLOAD", payload)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nif [ \"$AWS_CONFIG_FILE\" != \"$ATTESTRA_TEST_CONFIG\" ]; then echo recursion-secret >&2; exit 1; fi\ncat \"$ATTESTRA_TEST_PAYLOAD\"\n"
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	source := credentialSource{Profile: "attestra", Region: "us-east-2", ConfigFile: filepath.Join(dir, "config"), CredentialsFile: filepath.Join(dir, "credentials"), SSO: true}
	t.Setenv("ATTESTRA_TEST_CONFIG", source.ConfigFile)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "wrong"))
	write := func(key string, expires time.Time) {
		t.Helper()
		data, _ := json.Marshal(processCredentials{Version: 1, AccessKeyId: key, SecretAccessKey: "private-secret", SessionToken: "private-token", Expiration: expires})
		if err := os.WriteFile(payload, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first", time.Now().Add(30*time.Second))
	provider := source.provider()
	first, err := provider.Retrieve(context.Background())
	if err != nil || first.AccessKeyID != "first" {
		t.Fatalf("first retrieval: %v", err)
	}
	write("renewed", time.Now().Add(time.Hour))
	next, err := provider.Retrieve(context.Background())
	if err != nil || next.AccessKeyID != "renewed" {
		t.Fatalf("renewal: %v", err)
	}
	if err := os.WriteFile(payload, []byte("private-secret invalid output"), 0600); err != nil {
		t.Fatal(err)
	}
	cached, err := provider.Retrieve(context.Background())
	if err != nil || cached.AccessKeyID != "renewed" {
		t.Fatal("fresh credentials were not cached")
	}
	_, err = source.export(context.Background())
	if err == nil || !strings.Contains(err.Error(), "aws sso login") || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unsafe/unhelpful error: %v", err)
	}
}

func TestProfileSourceRelativePathsMatchLoginDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", "custom/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "custom/credentials")
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", "custom/cache")
	source, err := profileSource(options{Root: root, Profile: "attestra", Region: "us-east-2"})
	if err != nil {
		t.Fatal(err)
	}
	if source.ConfigFile != filepath.Join(root, "custom/config") || source.CredentialsFile != filepath.Join(root, "custom/credentials") || source.LoginCache != filepath.Join(root, "custom/cache") {
		t.Fatalf("relative paths do not match login: %+v", source)
	}
}

func TestRenewalFailureDoesNotExposeStderr(t *testing.T) {
	source := credentialSource{Profile: "attestra", Region: "us-east-2", ConfigFile: filepath.Join(t.TempDir(), "config"), CredentialsFile: filepath.Join(t.TempDir(), "credentials"), SSO: true}
	env := []string{sourceEnvironmentKey + "=" + source.encode()}
	err := credentialRenewalFailure(env, "secret AWS credentials could not be renewed for profile secret")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "aws sso login") {
		t.Fatalf("unsafe recovery: %v", err)
	}
	if credentialRenewalFailure(env, "AccessDenied") != nil {
		t.Fatal("unrelated error misclassified")
	}
}
