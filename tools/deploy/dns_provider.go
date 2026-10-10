package main

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/charmbracelet/huh"
)

// Existing Pulumi configuration takes precedence over remembered UI selections.
// Legacy blank Route53 selections meant externally managed DNS (Cloudflare).
func dnsSelection(configZone, provider, zone string, zoneKnown bool) (string, string, error) {
	if configZone != "" {
		return "route53", configZone, nil
	}
	if provider == "" && zoneKnown {
		provider = "cloudflare"
		if zone != "" {
			provider = "route53"
		}
	}
	if provider != "" && provider != "route53" && provider != "cloudflare" {
		return "", "", errors.New("invalid saved DNS provider; expected route53 or cloudflare")
	}
	if provider == "cloudflare" {
		zone = ""
	}
	return provider, zone, nil
}
func askDNSProvider(provider, zone string) (string, string, error) {
	if provider == "" {
		provider = "route53"
		if err := huh.NewSelect[string]().Title("Which service manages DNS for your domain?").Description("Use the provider that hosts your domain's authoritative DNS records.").Options(huh.NewOption("Amazon Route 53", "route53"), huh.NewOption("Cloudflare", "cloudflare")).Value(&provider).Run(); err != nil {
			return "", "", err
		}
	}
	if provider == "route53" {
		if zone == "" {
			if err := input("Route 53 public hosted zone ID", &zone, false, true).Description("Use the existing authoritative zone containing both the email and API domains.").Validate(validateHostedZoneID).Run(); err != nil {
				return "", "", err
			}
		}
		if err := validateHostedZoneID(zone); err != nil {
			return "", "", err
		}
		fmt.Println("DNS provider: Amazon Route 53 • hosted zone " + zone)
	} else {
		zone = ""
		fmt.Println("DNS provider: Cloudflare • an API token will be requested when DNS records are ready.")
	}
	return provider, zone, nil
}
func validateHostedZoneID(zone string) error {
	if !regexp.MustCompile(`^Z[A-Z0-9]+$`).MatchString(zone) {
		return errors.New("enter a hosted zone ID beginning with Z")
	}
	return nil
}
func (w *bootstrapWizard) selectDNSProvider(configZone string) (string, error) {
	provider, _ := w.remembered("dnsProvider")
	zone, known := w.remembered("route53")
	provider, zone, err := dnsSelection(configZone, provider, zone, known)
	if err != nil {
		return "", err
	}
	provider, zone, err = askDNSProvider(provider, zone)
	if err != nil {
		return "", err
	}
	// Save together so an interruption cannot remember a provider without its zone.
	if w.memory.Settings == nil {
		w.memory.Settings = map[string]string{}
	}
	w.memory.Settings["dnsProvider"], w.memory.Settings["route53"] = provider, zone
	return zone, w.saveMemory()
}
