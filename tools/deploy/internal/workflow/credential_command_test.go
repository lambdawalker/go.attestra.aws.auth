package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestCredentialExecutableWindows(t *testing.T) {
	short := func(string) (string, error) { return `C:\Users\DAVID~1\Temp\deploy.exe`, nil }
	for _, path := range []string{`C:\Users\David Smith\Temp\deploy.exe`, `C:\Users\D\Temp\deploy.exe`} {
		got, err := credentialExecutable(path, "windows", short)
		if err != nil || strings.ContainsAny(got, "\" \\") {
			t.Fatalf("unsafe Windows command %q: %v", got, err)
		}
	}
	got, err := credentialExecutable(`C:\Users\D\Temp\deploy.exe`, "windows", nil)
	if err != nil || got != "C:/Users/D/Temp/deploy.exe" {
		t.Fatalf("plain path: %q %v", got, err)
	}
}

func TestCredentialExecutableFailsClosedWithoutShortPath(t *testing.T) {
	for _, path := range []string{`C:\Users\David Smith\deploy.exe`, `C:\temp\a&b\deploy.exe`, `C:\Users\O'Connor\deploy.exe`} {
		for _, resolve := range []func(string) (string, error){
			func(p string) (string, error) { return p, nil },
			func(string) (string, error) { return "", errors.New("unavailable") },
		} {
			if _, err := credentialExecutable(path, "windows", resolve); err == nil {
				t.Fatal("accepted an unsafe Windows command")
			}
		}
	}
}

func TestCredentialExecutableUnix(t *testing.T) {
	got, err := credentialExecutable("/tmp/build with spaces/deploy", "linux", nil)
	if err != nil || got != `"/tmp/build with spaces/deploy"` {
		t.Fatalf("Unix path: %q %v", got, err)
	}
}
