package android

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/registry"
)

// Write saves the public settings consumed by the Android Gradle build.
func Write(path, environment, linkHost, kind string, deployed registry.Configuration) error {
	values := [][2]string{
		{"attestraEnvironment", environment}, {"attestraApiBaseUrl", deployed.APIURL}, {"attestraLinkHost", linkHost},
		{"attestraAwsRegion", deployed.AWSRegion}, {"attestraCognitoUserPoolId", deployed.CognitoUserPoolID}, {"attestraCognitoClientId", deployed.CognitoClientID},
		{"attestraCaptureEnabled", strconv.FormatBool(deployed.Features.IDCapture)}, {"attestraCaptureDocumentType", kind},
	}
	var body strings.Builder
	body.WriteString("# Generated public Attestra configuration. Contains no credentials.\n# Server document policy remains authoritative for capture.\n")
	escape := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	for _, item := range values {
		fmt.Fprintf(&body, "%s=%s\n", item[0], escape.Replace(item[1]))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".android-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(body.String()); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	fmt.Println("✓ Android configuration exported:", path)
	return nil
}
