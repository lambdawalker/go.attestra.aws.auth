package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAndroidExportContainsOnlyPublicSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa.properties")
	if err := exportAndroidConfiguration(healthRunner{}, options{Stack: "qa", Region: "us-east-2"}, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"attestraEnvironment=qa", "attestraApiBaseUrl=https://qa.api.example.com", "attestraCognitoClientId=client123", "attestraCaptureEnabled=false", "attestraLinkHost=qa.example.com"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(data), "proofKey") || strings.Contains(string(data), "private") {
		t.Fatal("nonpublic settings exported")
	}
	before := string(data)
	if err := exportAndroidConfiguration(healthRunner{fail: "stack output"}, options{Stack: "qa", Region: "us-east-2"}, path); err == nil {
		t.Fatal("export ignored failed outputs")
	}
	data, _ = os.ReadFile(path)
	if string(data) != before {
		t.Fatal("failed export replaced last valid configuration")
	}
}
