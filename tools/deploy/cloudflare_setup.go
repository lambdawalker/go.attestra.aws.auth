package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
)

func (w *bootstrapWizard) configureCloudflare() error {
	// Refuse two controllers managing the same SES DNS records.
	data, err := w.r.Exec(filepath.Join(w.root, "infra"), true, "pulumi", "config", "--json", "--stack", w.o.Stack)
	if err != nil {
		return err
	}
	var config map[string]struct{ Value string }
	if json.Unmarshal(data, &config) != nil {
		return errors.New("cannot read stack configuration")
	}
	if config["attestra-auth-email:route53ZoneId"].Value != "" {
		return errors.New("this stack configures DNS through Route53; review that configuration before using Cloudflare")
	}
	evidence, err := w.readSES()
	if err != nil {
		return err
	}
	if len(evidence.Records) != 4 {
		return errors.New("SES verification TXT and all three DKIM tokens must exist; finish creating the SES identity with deployment, then retry")
	}
	fmt.Printf("Cloudflare DNS • environment %s • AWS account %s • region %s • identity %s\n", w.environment, w.account, w.o.Region, evidence.Domain)
	fmt.Println("Create a Cloudflare API token with Zone:Read and DNS:Edit, limited to the authoritative zone. Token stays in memory and is never saved to GitHub, Pulumi, or a local file.")
	token := ""
	if err = input("Cloudflare API token (hidden)", &token, true, true).Run(); err != nil {
		return err
	}
	client := newCloudflareClient(strings.TrimSpace(token))
	w.cf = client
	zones, err := client.zones(evidence.Domain)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		return errors.New("no active matching zone accessible to this token; check zone scope and DNS delegation")
	}
	choices := make([]huh.Option[string], 0, len(zones))
	for _, zone := range zones {
		choices = append(choices, huh.NewOption(zone.Name+" ("+zone.ID+")", zone.ID))
	}
	selected := zones[0].ID
	if err = huh.NewSelect[string]().Title("Authoritative Cloudflare zone").Options(choices...).Value(&selected).Run(); err != nil {
		return err
	}
	var zone cloudflareZone
	for _, candidate := range zones {
		if candidate.ID == selected {
			zone = candidate
		}
	}
	if zone.ID == "" {
		return errors.New("invalid zone selection")
	}
	for _, record := range evidence.Records {
		if !withinZone(record.Name, zone.Name) {
			return errors.New("SES record is outside the selected zone")
		}
	}
	plan, err := client.plan(selected, evidence.Records)
	if err != nil {
		return err
	}
	mutations := false
	for _, change := range plan {
		fmt.Printf("%-8s %-5s %s → %s\n", change.Action, change.Record.Type, change.Record.Name, change.Record.Content)
		mutations = mutations || change.Action != "keep"
	}
	if mutations {
		fmt.Println("Applying SES DNS changes to " + zone.Name + "; CNAMEs will be DNS only and unrelated records are preserved.")
		if err = client.apply(selected, plan); err != nil {
			return fmt.Errorf("DNS setup stopped; completed changes retained, rerun to resume: %w", err)
		}
	}
	fmt.Println("✓ Cloudflare records match SES. AWS verification may take time; choose the DNS check again, then resume deployment. Website DNS/hosting and SES sandbox access are separate.")
	return w.showDNS()
}
