package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func dnsFingerprint(e sesEvidence) string {
	data, _ := json.Marshal(e.Records)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func sesReady(e sesEvidence) bool { return e.Verification == "Success" && e.DKIM == "Success" }

// Target only prerequisites; never include their dependents (Cognito).
func sesTargets(stack string, route53 bool) []string {
	prefix := "urn:pulumi:" + stack + "::attestra-auth-email::"
	targets := []string{prefix + "aws:ses/domainIdentity:DomainIdentity::email-sender", prefix + "aws:ses/domainDkim:DomainDkim::email-sender-dkim"}
	if route53 {
		targets = append(targets, prefix+"aws:route53/record:Record::ses-verify")
		for n := 0; n < 3; n++ {
			targets = append(targets, fmt.Sprintf("%saws:route53/record:Record::ses-dkim-%d", prefix, n))
		}
	}
	return targets
}

func waitSES(ctx context.Context, interval time.Duration, read func() (sesEvidence, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("verification wait stopped; rerun setup to resume: %w", err)
		}
		evidence, err := read()
		if err != nil {
			return err
		}
		fmt.Printf("SES %s • Verification: %s • DKIM: %s\n", evidence.Domain, evidence.Verification, evidence.DKIM)
		if sesReady(evidence) {
			return nil
		}
		if evidence.Verification == "Failed" || evidence.DKIM == "Failed" {
			return errors.New("SES verification failed; check DNS records and retry verification in SES before resuming")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("verification wait stopped; rerun setup to resume: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (w *bootstrapWizard) deployStages() error {
	before, _ := readLocalRevision(w.root)
	configuration := w.configurationDigest()
	if err := w.r.withIndex(w.o, w.deployStagesActual); err != nil {
		return err
	}
	if err := w.summaryStep("publication"); err != nil {
		return err
	}
	if err := w.checkSummaryHealth(); err != nil {
		return err
	}
	if err := exportAndroidConfiguration(w.r, w.o, defaultAndroidExport(w.o)); err != nil {
		return err
	}
	return w.recordCompletion(before, configuration)
}
func (w *bootstrapWizard) deployStagesActual() error {
	if err := w.markStage("deploying"); err != nil {
		return err
	}
	data, err := w.r.Exec(filepath.Join(w.root, "infra"), true, "pulumi", "config", "--json", "--stack", w.o.Stack)
	if err != nil {
		return err
	}
	var config map[string]struct{ Value string }
	if json.Unmarshal(data, &config) != nil {
		return errors.New("cannot read stack configuration")
	}
	route53 := config["attestra-auth-email:route53ZoneId"].Value != ""
	fmt.Println("1 / 4 • Build and deploy SES prerequisites only")
	partial := w.o
	partial.CI = "deploy"
	partial.Targets = sesTargets(w.o.Stack, route53)
	apiDomain := config["attestra-auth-email:apiDomain"].Value
	if apiDomain != "" {
		prefix := "urn:pulumi:" + w.o.Stack + "::attestra-auth-email::"
		partial.Targets = append(partial.Targets, prefix+"aws:acm/certificate:Certificate::api-certificate")
		if route53 {
			partial.Targets = append(partial.Targets, prefix+"aws:route53/record:Record::api-certificate-dns")
		}
	}
	if err := execute(w.r, partial); err != nil {
		return err
	}
	if err := w.markStage("ses-prerequisites"); err != nil {
		return err
	}
	evidence, err := w.readSES()
	if err != nil {
		return err
	}
	if !sesReady(evidence) {
		fmt.Println("2 / 4 • Configure verification DNS")
		fingerprint, _ := w.remembered("dnsFingerprint")
		if !route53 && fingerprint != dnsFingerprint(evidence) {
			if err := w.configureCloudflare(); err != nil {
				return err
			}
			if err := w.remember("dnsFingerprint", dnsFingerprint(evidence)); err != nil {
				return err
			}
		}
		fmt.Println("3 / 4 • Waiting for Amazon SES verification; checking every 15 seconds. Ctrl+C stops this wait safely. Rerun setup to resume; existing resources are retained.")
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		err = waitSES(ctx, 15*time.Second, w.readSES)
		stop()
		if err != nil {
			return err
		}
	} else {
		fmt.Println("2–3 / 4 • SES and DKIM already verified; DNS setup and wait skipped")
	}
	if apiDomain != "" {
		fmt.Println("Validate HTTPS certificate for " + apiDomain)
		if err := w.prepareAPICertificate(apiDomain, route53); err != nil {
			return err
		}
	}
	fmt.Println("4 / 4 • Preview and deploy the complete application")
	if err := w.markStage("ses-verified"); err != nil {
		return err
	}
	full := w.o
	full.ReuseBuild = true
	full.SkipIndex = true
	full.CI = "deploy"
	if err := execute(w.r, full); err != nil {
		return err
	}
	if apiDomain != "" {
		if err := w.publishAPIDomain(apiDomain, route53); err != nil {
			return err
		}
	}
	return w.markStage("deployed")
}
