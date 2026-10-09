package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func packageLambda(binary, archive string) (err error) {
	in, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(archive), "lambda-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	z := zip.NewWriter(out)
	header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	header.SetMode(0755)
	entry, err := z.CreateHeader(header)
	if err == nil {
		_, err = io.Copy(entry, in)
	}
	if e := z.Close(); err == nil {
		err = e
	}
	if e := out.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	// Windows cannot rename over an existing destination.
	if err = os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(out.Name(), archive)
}
func buildLambdas(root string) error {
	names := []string{"signup", "resend", "confirm", "challenge", "passkeyoptions", "passkeycomplete", "capture", "capture-worker", "capture-dispatcher", "auth-email-start", "auth-email-complete", "auth-passkey-start", "auth-passkey-complete", "auth-refresh", "auth-status"}
	for _, name := range names {
		source := "./cmd/" + name
		if len(name) > 5 && name[:5] == "auth-" {
			source = "./cmd/signin"
		}
		dir := filepath.Join(root, "dist", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		binary := filepath.Join(dir, "bootstrap")
		cmd := exec.Command("go", "build", "-trimpath", "-tags", "lambda.norpc", "-o", binary, source)
		cmd.Dir = root
		cmd.Env = append(withoutCredentials(os.Environ()), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		fmt.Println("Building", name)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", name, err)
		}
		if err := packageLambda(binary, filepath.Join(root, "dist", name+".zip")); err != nil {
			return err
		}
	}
	return nil
}
