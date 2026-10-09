package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise real launchers with a fake Go executable: no cloud or build calls.
func TestDirectLaunchers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "repo with spaces")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "trace.txt")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DEPLOY_TEST_TRACE\"\nexit 23\n"
	goName, ext := "go", ".sh"
	if runtime.GOOS == "windows" {
		goName, ext = "go.cmd", ".bat"
		stub = "@echo off\r\n> \"%DEPLOY_TEST_TRACE%\" echo %*\r\nexit /b 23\r\n"
	}
	if err = os.WriteFile(filepath.Join(dir, goName), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	for name, flag := range map[string]string{"deploy": "-backend", "setup": "-bootstrap", "setup-github": "-bootstrap", "build": "-build"} {
		data, e := os.ReadFile(filepath.Join(root, name+ext))
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(dir, name+ext)
		if e = os.WriteFile(path, data, 0700); e != nil {
			t.Fatal(e)
		}
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd.exe", "/d", "/c", path, "-backend", "s3://test-state")
		} else {
			cmd = exec.Command("sh", path, "-backend", "s3://test-state")
		}
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "DEPLOY_TEST_TRACE="+trace)
		out, e := cmd.CombinedOutput()
		var exit *exec.ExitError
		if e == nil {
			t.Fatal("launcher swallowed failing exit", name)
		} else if !errors.As(e, &exit) || exit.ExitCode() != 23 {
			t.Fatalf("%s: %v %s", name, e, out)
		}
		b, e := os.ReadFile(trace)
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		for _, part := range []string{"-C", "tools", "deploy", "run", "-repo-root", dir, flag, "s3://test-state"} {
			if !strings.Contains(s, part) {
				t.Fatalf("%s missing %s: %s", name, part, s)
			}
		}
	}
}
