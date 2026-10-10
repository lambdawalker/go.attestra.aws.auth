package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
)

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
	partial.Targets = sesTargets(w.o.Stack, route53)
	if err := execute(w.r, partial); err != nil {
		return err
	}
	evidence, err := w.readSES()
	if err != nil {
		return err
	}
	if !sesReady(evidence) {
		fmt.Println("2 / 4 • Configure verification DNS")
		if !route53 {
			choice := "cloudflare"
			if err := huh.NewSelect[string]().Title("Publish SES verification records").Options(huh.NewOption("Configure Cloudflare DNS", "cloudflare"), huh.NewOption("DNS already configured / configure manually", "manual"), huh.NewOption("Pause setup; resume later", "pause")).Value(&choice).Run(); err != nil {
				return err
			}
			switch choice {
			case "pause":
				return errors.New("setup paused; SES prerequisites retained; rerun Build, preview and deploy to resume")
			case "cloudflare":
				if err := w.configureCloudflare(); err != nil {
					return err
				}
			case "manual":
				if err := w.showDNS(); err != nil {
					return err
				}
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
	fmt.Println("4 / 4 • Preview and deploy the complete application")
	return execute(w.r, w.o)
}
