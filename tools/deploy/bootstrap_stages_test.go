package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSESPrerequisiteTargets(t *testing.T) {
	for _, route53 := range []bool{false, true} {
		targets := sesTargets("qa", route53)
		want := 2
		if route53 {
			want = 6
		}
		if len(targets) != want {
			t.Fatal(targets)
		}
		r := goodRunner()
		o := testOptions()
		o.Stack, o.Targets, o.CI = "qa", targets, "deploy"
		if err := execute(r, o); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.calls[len(r.calls)-1], "--yes --non-interactive") {
			t.Fatal("deployment still prompts", r.calls)
		}
		for _, call := range r.calls[4:] {
			if strings.Contains(call, "--target-dependents") {
				t.Fatal(call)
			}
			for _, target := range targets {
				if !strings.Contains(call, "--target "+target) {
					t.Fatal(call)
				}
			}
		}
	}
}

func TestWaitSESRequiresBothSuccess(t *testing.T) {
	calls := 0
	err := waitSES(context.Background(), time.Millisecond, func() (sesEvidence, error) {
		calls++
		e := sesEvidence{Verification: "Success", DKIM: "Pending"}
		if calls == 2 {
			e.DKIM = "Success"
		}
		return e, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("%v calls %d", err, calls)
	}
}

func TestWaitSESStopsOnFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"error", "failed", "cancel"} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := waitSES(ctx, time.Hour, func() (sesEvidence, error) {
			calls++
			if mode == "error" {
				return sesEvidence{}, errors.New("access denied")
			}
			if mode == "failed" {
				return sesEvidence{Verification: "Failed"}, nil
			}
			cancel()
			return sesEvidence{Verification: "Pending"}, nil
		})
		cancel()
		if err == nil || calls != 1 {
			t.Fatalf("%s: %v calls %d", mode, err, calls)
		}
	}
}

func TestDNSMemoryInvalidatesWhenTokensChange(t *testing.T) {
	e := sesEvidence{Records: []dnsRecord{{Type: "TXT", Name: "_amazonses.dev.example.com", Content: "old"}}}
	original := dnsFingerprint(e)
	e.Verification = "Success"
	if dnsFingerprint(e) != original {
		t.Fatal("status changed DNS fingerprint")
	}
	e.Records[0].Content = "new"
	if dnsFingerprint(e) == original {
		t.Fatal("replacement identity reused old DNS completion")
	}
}
