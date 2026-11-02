package workflow

import (
	"errors"
	"testing"
)

func TestNukeCLIRequiresExclusiveTeardown(t *testing.T) {
	for _, pair := range [][2]bool{{false, false}, {false, true}, {true, false}} {
		if validateNukeFlags(pair[0], pair[1], []string{"check"}) == nil {
			t.Fatal("check accepted without teardown and nuke")
		}
	}
	if err := validateNukeFlags(true, true, []string{"teardown", "nuke", "check"}); err != nil {
		t.Fatal(err)
	}
	if validateNukeFlags(true, false, []string{"nuke"}) == nil {
		t.Fatal("standalone nuke accepted")
	}
	for _, flag := range []string{"ci", "scope", "teardown-index", "build", "bootstrap", "stack"} {
		if validateNukeFlags(true, true, []string{"teardown", "nuke", flag}) == nil {
			t.Fatal("conflicting flag accepted: " + flag)
		}
	}
	if err := validateNukeFlags(true, true, []string{"teardown", "nuke", "repo-root"}); err != nil {
		t.Fatal(err)
	}
}

func TestNukeAbsentIsSuccess(t *testing.T) {
	deleted := false
	r := nukeStep("missing", func() (bool, error) { return false, nil }, func() error { deleted = true; return nil })
	if r.Status != "ABSENT" || deleted || !nukeSuccessful([]nukeResult{r}) {
		t.Fatalf("absent must succeed without a delete: %+v, deleted=%t", r, deleted)
	}
}

func TestNukeDeleteMustBeVerified(t *testing.T) {
	r := nukeStep("still here", func() (bool, error) { return true, nil }, func() error { return nil })
	if r.Status != "REMAINS" || nukeSuccessful([]nukeResult{r}) {
		t.Fatalf("unverified deletion accepted: %+v", r)
	}
	checks := 0
	r = nukeStep("gone", func() (bool, error) { checks++; return checks == 1, nil }, func() error { return nil })
	if r.Status != "DELETED" {
		t.Fatal(r)
	}
}

func TestNukeRecoveryGate(t *testing.T) {
	for _, status := range []string{"FAILED", "REMAINS", "UNVERIFIED", "RETAINED", "unexpected"} {
		t.Run(status, func(t *testing.T) {
			called := false
			nukeRecovery([]nukeResult{{Status: status}}, func() { called = true })
			if called {
				t.Fatal("recovery artifacts deleted after " + status)
			}
		})
	}
	called := false
	nukeRecovery([]nukeResult{{Status: "ABSENT"}, {Status: "DELETED"}, {Status: "INFO"}}, func() { called = true })
	if !called {
		t.Fatal("confirmed absence blocked recovery cleanup")
	}
}

func TestNukeContinuesAfterFailureAndRejectsUnknownAbsence(t *testing.T) {
	var results []nukeResult
	results = append(results, nukeStep("denied", func() (bool, error) { return false, errors.New("AccessDenied") }, func() error { t.Fatal("delete on unreadable identity"); return nil }))
	results = append(results, nukeStep("missing", func() (bool, error) { return false, nil }, func() error { return nil }))
	if results[0].Status != "FAILED" || results[1].Status != "ABSENT" || nukeSuccessful(results) {
		t.Fatal(results)
	}
}
