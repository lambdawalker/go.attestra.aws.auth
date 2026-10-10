package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the same cmd.exe invocation used by the AWS Go SDK.
func TestWindowsCredentialCommandLaunch(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plain", "with spaces"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "helper.exe")
			if err := os.WriteFile(target, binary, 0700); err != nil {
				t.Fatal(err)
			}
			command, err := credentialExecutable(target, "windows", shortCredentialPath)
			if err != nil {
				// Filesystems may disable 8.3 aliases: require clear recovery guidance.
				if !strings.Contains(err.Error(), "set TEMP and TMP") {
					t.Fatal(err)
				}
				t.Log(err)
				return
			}
			cmd := exec.Command("cmd.exe", "/C", command+" -test.run=TestWindowsCredentialHelperChild")
			cmd.Env = append(os.Environ(), "ATTESTRA_TEST_CREDENTIAL_CHILD=1")
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "helper-launched" {
				t.Fatalf("launch: %v; %s", err, out)
			}
		})
	}
}

func TestWindowsCredentialHelperChild(t *testing.T) {
	if os.Getenv("ATTESTRA_TEST_CREDENTIAL_CHILD") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("helper-launched")
	os.Exit(0)
}
