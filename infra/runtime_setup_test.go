package main

import (
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestMissingRuntimeSetupReturnsGuidanceBeforeResources(t *testing.T) {
	t.Setenv("PULUMI_CONFIG", "{}")
	m := &associationMocks{resources: map[string]pulumi.MockResourceArgs{}}
	err := pulumi.RunErr(deploy, pulumi.WithMocks("attestra-auth-email", "dev", m))
	if err == nil || !strings.Contains(err.Error(), "setup.bat -Stack dev -force-setup") {
		t.Fatalf("expected actionable setup error instead of config panic, got %v", err)
	}
	for _, r := range m.resources {
		if strings.HasPrefix(r.TypeToken, "aws:") {
			t.Fatalf("registered %s before setup validation", r.TypeToken)
		}
	}
}
