package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func (w *bootstrapWizard) output(name string) (string, error) {
	data, err := w.r.Exec(filepath.Join(w.root, "infra"), true, "pulumi", "stack", "output", name, "--stack", w.o.Stack)
	return strings.TrimSpace(string(data)), err
}

// Uses the existing conflict checks and read-back verification for CNAMEs too.
func (w *bootstrapWizard) publishDNS(domain string, desired []dnsRecord) error {
	if err := w.cloudflareClient(); err != nil {
		return err
	}
	zones, err := w.cf.zones(domain)
	if err != nil {
		return err
	}
	// Prefer the most specific active zone containing the requested name.
	var zone cloudflareZone
	for _, z := range zones {
		if len(z.Name) > len(zone.Name) {
			zone = z
		}
	}
	if zone.ID == "" {
		return errors.New("no accessible Cloudflare zone for API domain")
	}
	for _, record := range desired {
		if !withinZone(record.Name, zone.Name) {
			return errors.New("DNS record outside API zone")
		}
	}
	plan, err := w.cf.plan(zone.ID, desired)
	if err != nil {
		return err
	}
	for _, change := range plan {
		fmt.Printf("%-8s %s %s → %s\n", change.Action, change.Record.Type, change.Record.Name, change.Record.Content)
	}
	return w.cf.apply(zone.ID, plan)
}

type apiCertificateStatus struct {
	Certificate struct {
		DomainName, Status      string
		DomainValidationOptions []struct {
			ResourceRecord struct{ Name, Type, Value string }
		}
	}
}

func (w *bootstrapWizard) certificateStatus(arn string) (apiCertificateStatus, error) {
	var result apiCertificateStatus
	_, err := w.a.call(&result, "acm", "describe-certificate", "--certificate-arn", arn)
	return result, err
}
func (w *bootstrapWizard) prepareAPICertificate(domain string, route53 bool) error {
	arn, err := w.output("apiCertificateArn")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	published := false
	for {
		if ctx.Err() != nil {
			return errors.New("certificate wait interrupted; rerun setup to resume")
		}
		status, err := w.certificateStatus(arn)
		if err != nil {
			return err
		}
		if status.Certificate.DomainName != domain {
			return errors.New("certificate domain differs from configured API domain")
		}
		fmt.Println("API certificate: " + status.Certificate.Status)
		if status.Certificate.Status == "ISSUED" {
			return nil
		}
		if status.Certificate.Status != "PENDING_VALIDATION" {
			return fmt.Errorf("certificate status %s; inspect ACM before retrying", status.Certificate.Status)
		}
		records := []dnsRecord{}
		for _, option := range status.Certificate.DomainValidationOptions {
			r := option.ResourceRecord
			if r.Name != "" && r.Type == "CNAME" && r.Value != "" {
				records = append(records, dnsRecord{Type: r.Type, Name: strings.TrimSuffix(r.Name, "."), Content: r.Value})
			}
		}
		if !route53 && !published && len(records) > 0 {
			if err := w.publishDNS(domain, records); err != nil {
				return err
			}
			published = true
		}
		fmt.Println("Waiting for ACM; next check in 15 seconds. Ctrl+C pauses safely.")
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("certificate wait interrupted; rerun setup to resume")
		case <-timer.C:
		}
	}
}
func (w *bootstrapWizard) publishAPIDomain(domain string, route53 bool) error {
	if route53 {
		return nil
	}
	target, err := w.output("apiDomainTarget")
	if err != nil {
		return err
	}
	if target == "" || strings.Contains(target, "unknown") {
		return errors.New("API custom domain target is unavailable")
	}
	return w.publishDNS(domain, []dnsRecord{{Type: "CNAME", Name: domain, Content: target}})
}
