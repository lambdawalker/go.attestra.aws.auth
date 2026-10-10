package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCaptureReservationCapacity(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		requested, total, unreserved int
		existing                     [3]int
		want, additional, available  int
	}{
		{"small QA account", 5, 10, 10, [3]int{}, -1, 15, 0},
		{"exact headroom", 5, 1000, 115, [3]int{}, 5, 15, 15},
		{"one short", 5, 1000, 114, [3]int{}, -1, 15, 14},
		{"existing reservations do not need new capacity", 5, 1000, 100, [3]int{5, 5, 5}, 5, 0, 0},
		{"increase only", 10, 1000, 115, [3]int{5, 5, 5}, 10, 15, 15},
		{"partial previous deploy", 5, 1000, 110, [3]int{5, 0, 0}, 5, 10, 10},
		{"do not spend pending decreases", 5, 1000, 100, [3]int{20, 0, 0}, -1, 10, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, needed, available, err := captureCapacity(tc.requested, tc.total, tc.unreserved, tc.existing)
			if err != nil || got != tc.want || needed != tc.additional || available != tc.available {
				t.Fatal(got, needed, available, err)
			}
		})
	}
	if _, _, _, err := captureCapacity(5, 10, 11, [3]int{}); err == nil {
		t.Fatal("invalid quota accepted")
	}
}

type capacityRunner struct {
	config, state, account string
	fail                   string
	calls                  []string
}

func (r *capacityRunner) Exec(_ string, _ bool, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if r.fail != "" && strings.Contains(call, r.fail) {
		return nil, errors.New("denied")
	}
	switch {
	case strings.Contains(call, "config --json"):
		return []byte(r.config), nil
	case strings.Contains(call, "stack export"):
		return []byte(r.state), nil
	case strings.Contains(call, "get-account-settings"):
		return []byte(r.account), nil
	case strings.Contains(call, "get-function-concurrency"):
		return []byte(`{"ReservedConcurrentExecutions":5}`), nil
	}
	return nil, errors.New("unexpected command")
}
func TestCapturePreflightFallbackWithoutConfigWrites(t *testing.T) {
	r := &capacityRunner{config: `{"aws:region":{"value":"us-east-2"},"attestra-auth-email:captureReservedConcurrency":{"value":"5"}}`, state: `{"deployment":{"resources":[]}}`, account: `{"AccountLimit":{"ConcurrentExecutions":10,"UnreservedConcurrentExecutions":10}}`}
	got, err := captureConcurrencyPreflight(r, "/repo/infra", "qa")
	if err != nil || got != -1 {
		t.Fatal(got, err)
	}
	if len(r.calls) != 3 {
		t.Fatal(r.calls)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "config set") || strings.Contains(call, "put-function") {
			t.Fatal("mutated state", call)
		}
	}
	r.fail = "get-account-settings"
	if _, err = captureConcurrencyPreflight(r, "/repo/infra", "qa"); err == nil || !strings.Contains(err.Error(), "GetAccountSettings") {
		t.Fatal(err)
	}
	r.fail = ""
	r.account = `{}`
	if _, err = captureConcurrencyPreflight(r, "/repo/infra", "qa"); err == nil {
		t.Fatal("invalid response accepted")
	}
	r.config = `{"attestra-auth-email:captureReservedConcurrency":{"value":"-1"}}`
	r.calls = nil
	if got, err = captureConcurrencyPreflight(r, "/repo/infra", "qa"); err != nil || got != -1 || len(r.calls) != 1 {
		t.Fatal(got, err, r.calls)
	}
}
func TestCapturePreflightCreditsOnlyOwnedLiveReservations(t *testing.T) {
	r := &capacityRunner{config: `{"aws:region":{"value":"us-east-2"},"attestra-auth-email:captureReservedConcurrency":{"value":"5"}}`, state: `{"deployment":{"resources":[{"type":"aws:lambda/function:Function","urn":"urn:pulumi:qa::attestra-auth-email::aws:lambda/function:Function::capture","id":"capture-owned"},{"type":"aws:lambda/function:Function","urn":"urn:pulumi:qa::attestra-auth-email::aws:lambda/function:Function::email-signup","id":"unrelated"}]}}`, account: `{"AccountLimit":{"ConcurrentExecutions":1000,"UnreservedConcurrentExecutions":110}}`}
	got, err := captureConcurrencyPreflight(r, "/repo/infra", "qa")
	if err != nil || got != 5 {
		t.Fatal(got, err)
	}
	calls := strings.Join(r.calls, "\n")
	if !strings.Contains(calls, "--function-name capture-owned") || strings.Contains(calls, "--function-name unrelated") {
		t.Fatal(calls)
	}
}
func TestCaptureEnvironmentDoesNotMutateRunnerCredentials(t *testing.T) {
	before := []string{"AWS_REGION=us-east-2", captureConcurrencyEnv + "=5"}
	after := captureEnvironment(before, -1)
	if before[1] != captureConcurrencyEnv+"=5" || len(after) != 2 || after[1] != captureConcurrencyEnv+"=-1" {
		t.Fatal(before, after)
	}
}

func TestProductionRequiresExplicitCapacityAndRejectsFallback(t *testing.T) {
	r := &capacityRunner{config: `{"aws:region":{"value":"us-east-2"}}`}
	if _, err := captureConcurrencyPreflight(r, "/repo/infra", "prod"); err == nil {
		t.Fatal("production accepted implicit capacity")
	}
	r.config = `{"aws:region":{"value":"us-east-2"},"attestra-auth-email:environmentClass":{"value":"production"},"attestra-auth-email:captureReservedConcurrency":{"value":"5"},"attestra-auth-email:captureWorkerMaxConcurrency":{"value":"2"}}`
	r.state = `{"deployment":{"resources":[]}}`
	r.account = `{"AccountLimit":{"ConcurrentExecutions":10,"UnreservedConcurrentExecutions":10}}`
	if _, err := captureConcurrencyPreflight(r, "/repo/infra", "live"); err == nil {
		t.Fatal("production silently fell back")
	}
}
func TestTestEnvironmentDefaultsUseSharedCapacity(t *testing.T) {
	for _, stack := range []string{"dev", "qa", "feature-test"} {
		r := &capacityRunner{config: `{}`}
		got, err := captureConcurrencyPreflight(r, "/repo/infra", stack)
		if err != nil || got != -1 || len(r.calls) != 1 {
			t.Fatalf("%s: %d %v", stack, got, err)
		}
	}
}
