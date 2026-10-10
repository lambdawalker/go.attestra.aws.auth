package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func exportAndroidConfiguration(r commandRunner, o options, path string) error {
	deployed, err := publicIndexConfiguration(r, o)
	if err != nil {
		return err
	}
	raw, err := r.Exec(filepath.Join(o.Root, "infra"), true, "pulumi", "config", "--json", "--stack", o.Stack)
	if err != nil {
		return err
	}
	var config map[string]struct{ Value string }
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("cannot read Android configuration")
	}
	origin, err := url.Parse(config["attestra-auth-email:appOrigin"].Value)
	if err != nil || !healthURL(origin.String()) {
		return errors.New("valid appOrigin is required for Android link handling")
	}
	kind := config["attestra-auth-email:captureDocumentType"].Value
	if kind == "" {
		kind = "sample_card"
	}
	values := [][2]string{
		{"attestraEnvironment", o.Stack}, {"attestraApiBaseUrl", deployed.APIURL}, {"attestraLinkHost", origin.Hostname()},
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
func defaultAndroidExport(o options) string {
	return filepath.Join(o.Root, "android-config", o.Stack+".properties")
}
