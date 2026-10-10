package main

import (
	"strings"
	"testing"
)

func TestDNSSelectionRetainsExistingAndLegacyChoices(t *testing.T) {
	for _, tc := range []struct {
		name, config, provider, zone string
		known                        bool
		wantProvider, wantZone       string
	}{
		{"new", "", "", "", false, "", ""},
		{"legacy cloudflare", "", "", "", true, "cloudflare", ""},
		{"legacy route53", "", "", "Z123", true, "route53", "Z123"},
		{"remembered cloudflare", "", "cloudflare", "", true, "cloudflare", ""},
		{"remembered route53", "", "route53", "Z123", true, "route53", "Z123"},
		{"actual config wins", "Z456", "cloudflare", "", true, "route53", "Z456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, zone, err := dnsSelection(tc.config, tc.provider, tc.zone, tc.known)
			if err != nil || provider != tc.wantProvider || zone != tc.wantZone {
				t.Fatal(provider, zone, err)
			}
		})
	}
	if _, _, err := dnsSelection("", "invalid", "", false); err == nil {
		t.Fatal("accepted invalid provider")
	}
}
func TestRememberedRoute53ChoiceConfiguresPulumiWithoutPrompt(t *testing.T) {
	w := &bootstrapWizard{root: t.TempDir(), environment: "qa", memory: bootstrapCheckpoint{Repository: "o/r", Environment: "qa", Stack: "qa", Settings: map[string]string{"route53": "Z123"}}}
	zone, err := w.selectDNSProvider("")
	if err != nil || zone != "Z123" {
		t.Fatal(zone, err)
	}
	if w.memory.Settings["dnsProvider"] != "route53" {
		t.Fatal(w.memory)
	}
	resumed := &bootstrapWizard{root: w.root, environment: "qa"}
	if err := resumed.loadSelections("o/r", map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
	if resumed.memory.Settings["dnsProvider"] != "route53" || resumed.memory.Settings["route53"] != "Z123" {
		t.Fatal("choice lost")
	}
	o := testOptions()
	o.Root = w.root
	o.Stack = "qa"
	f := &bootstrapFake{list: `[]`, config: `{"attestra-auth-email:route53ZoneId":{"value":""}}`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`}
	if err := bootstrapStack(f, o, map[string]string{"attestra-auth-email:route53ZoneId": zone}, false, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "attestra-auth-email:route53ZoneId -- Z123") {
		t.Fatal(f.calls)
	}
}
func TestRememberedCloudflareChoiceNeedsNoHostedZone(t *testing.T) {
	w := &bootstrapWizard{root: t.TempDir(), environment: "dev", memory: bootstrapCheckpoint{Settings: map[string]string{"route53": ""}}}
	zone, err := w.selectDNSProvider("")
	if err != nil || zone != "" || w.memory.Settings["dnsProvider"] != "cloudflare" {
		t.Fatal(zone, err, w.memory)
	}
	for _, zone := range []string{"", "abc", "Z123/other"} {
		if validateHostedZoneID(zone) == nil {
			t.Fatal("invalid zone accepted", zone)
		}
	}
}
