package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const captureConcurrencyEnv = "ATTESTRA_CAPTURE_RESERVED_CONCURRENCY"

// Only additional reservations require free capacity; reductions are not credited
// because Pulumi may update the three functions concurrently.
func captureCapacity(requested, total, unreserved int, existing [3]int) (effective, additional, available int, err error) {
	if requested < 1 || requested > int(^uint(0)>>1)/3 || total < 0 || unreserved < 0 || unreserved > total {
		return 0, 0, 0, errors.New("invalid Lambda concurrency settings")
	}
	available = max(0, unreserved-min(100, total))
	for _, current := range existing {
		if current < 0 || current > total {
			return 0, 0, 0, errors.New("invalid existing Lambda reservation")
		}
		increase := max(0, requested-current)
		additional += increase
	}
	effective = requested
	if additional > available {
		effective = -1
	}
	return
}
func captureConcurrencyPreflight(r commandRunner, dir, stack string) (int, error) {
	raw, err := r.Exec(dir, true, "pulumi", "config", "--json", "--stack", stack)
	if err != nil {
		return 0, err
	}
	var config map[string]struct{ Value string }
	if json.Unmarshal(raw, &config) != nil {
		return 0, errors.New("cannot read capture concurrency configuration")
	}
	requested := 5
	if value := config["attestra-auth-email:captureReservedConcurrency"].Value; value != "" {
		requested, err = strconv.Atoi(value)
		if err != nil || requested == 0 || requested < -1 {
			return 0, errors.New("captureReservedConcurrency must be -1 or a positive integer")
		}
	}
	if requested == -1 {
		return -1, nil
	}
	region := config["aws:region"].Value
	if region == "" {
		return 0, errors.New("aws:region is required for the Lambda quota check")
	}
	raw, err = r.Exec(dir, true, "pulumi", "stack", "export", "--stack", stack)
	if err != nil {
		return 0, err
	}
	var state struct {
		Deployment *struct {
			Resources []struct {
				URN, Type, ID string
				Delete        bool
			}
		}
	}
	if json.Unmarshal(raw, &state) != nil || state.Deployment == nil {
		return 0, errors.New("cannot read existing capture Lambda identities")
	}
	names := []string{"capture", "capture-worker", "capture-dispatcher"}
	existing := [3]int{}
	for _, resource := range state.Deployment.Resources {
		if resource.Type != "aws:lambda/function:Function" || resource.ID == "" || resource.Delete {
			continue
		}
		for i, name := range names {
			if !strings.HasSuffix(resource.URN, "::"+name) {
				continue
			}
			data, e := r.Exec(dir, true, "aws", "lambda", "get-function-concurrency", "--function-name", resource.ID, "--region", region, "--output", "json", "--no-cli-pager")
			if e != nil {
				return 0, fmt.Errorf("cannot check current reservation for %s; verify lambda:GetFunctionConcurrency access: %w", name, e)
			}
			var current struct{ ReservedConcurrentExecutions *int }
			if json.Unmarshal(data, &current) != nil {
				return 0, errors.New("invalid Lambda function concurrency response")
			}
			if current.ReservedConcurrentExecutions != nil {
				existing[i] = *current.ReservedConcurrentExecutions
			}
		}
	}
	// Read account headroom after function reservations, as close to the decision as possible.
	raw, err = r.Exec(dir, true, "aws", "lambda", "get-account-settings", "--region", region, "--output", "json", "--no-cli-pager")
	if err != nil {
		return 0, fmt.Errorf("cannot check Lambda quota; verify lambda:GetAccountSettings permission (rerun setup to update the deployment role): %w", err)
	}
	var account struct {
		AccountLimit struct{ ConcurrentExecutions, UnreservedConcurrentExecutions *int }
	}
	if json.Unmarshal(raw, &account) != nil || account.AccountLimit.ConcurrentExecutions == nil || account.AccountLimit.UnreservedConcurrentExecutions == nil {
		return 0, errors.New("invalid Lambda account concurrency response")
	}
	total, unreserved := *account.AccountLimit.ConcurrentExecutions, *account.AccountLimit.UnreservedConcurrentExecutions
	effective, additional, available, err := captureCapacity(requested, total, unreserved, existing)
	if err != nil {
		return 0, err
	}
	if effective == -1 {
		fmt.Printf("WARNING: capture requests %d reserved executions per function; %d additional needed, %d available after preserving the unreserved pool (quota %d, unreserved %d). Using shared concurrency for all three capture functions for this deployment. Requested configuration is unchanged; SQS worker maximum is unchanged.\n", requested, additional, available, total, unreserved)
	} else {
		fmt.Printf("Lambda quota check: %d additional reservations fit within %d available; keeping %d per capture function.\n", additional, available, requested)
	}
	return effective, nil
}
func captureEnvironment(env []string, effective int) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.EqualFold(strings.SplitN(entry, "=", 2)[0], captureConcurrencyEnv) {
			result = append(result, entry)
		}
	}
	return append(result, captureConcurrencyEnv+"="+strconv.Itoa(effective))
}
