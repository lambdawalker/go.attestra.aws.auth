package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestPulumiErrorShowsOperationAndRedactsSecrets(t *testing.T) {
	cause := errors.New("exit status 1")
	err := pulumiCommandError([]string{"stack", "ls", "--json"}, "error: AccessDenied reading .pulumi/meta.yaml\nsecret-pass session-value Bearer arbitrary-token", []string{"PULUMI_CONFIG_PASSPHRASE=secret-pass", "AWS_SESSION_TOKEN=session-value"}, cause)
	if !strings.Contains(err.Error(), "pulumi stack ls") || !strings.Contains(err.Error(), "AccessDenied") || !errors.Is(err, cause) {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-pass", "session-value", "arbitrary-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("leaked secret")
		}
	}
}
func TestPulumiConfigWritesNeverEchoValues(t *testing.T) {
	err := pulumiCommandError([]string{"config", "set", "proofKey", "private-proof", "--secret"}, "error: private-proof", nil, errors.New("exit 1"))
	if strings.Contains(err.Error(), "private-proof") {
		t.Fatal("leaked config value")
	}
}
func TestPulumiErrorBoundsAndStripsTerminalControl(t *testing.T) {
	err := pulumiCommandError([]string{"stack", "export"}, "\x1b[31merror:\x1b[0m "+strings.Repeat("x", 10000), nil, errors.New("exit 1"))
	if strings.Contains(err.Error(), "\x1b") || len(err.Error()) > 5000 {
		t.Fatal("unsafe/unbounded diagnostic")
	}
}

func TestPulumiErrorRedactsNormalizedCredentialText(t *testing.T) {
	err := pulumiCommandError([]string{"stack", "ls"}, "error: private-pass\r\n", []string{"PULUMI_CONFIG_PASSPHRASE=private-pass\r\n"}, errors.New("exit 1"))
	if strings.Contains(err.Error(), "private-pass") {
		t.Fatal("credential exposed after control normalization")
	}
}
