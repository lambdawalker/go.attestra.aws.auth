package main

import (
	"fmt"
	"strconv"
)

// parseCaptureConcurrency preserves the existing reservations unless the stack
// explicitly opts into the shared account pool with -1. Zero would disable the
// functions, so it is deliberately rejected.
func parseCaptureConcurrency(reservedValue, workerValue string) (int, int, error) {
	reserved, worker := 5, 5
	var err error
	if reservedValue != "" {
		reserved, err = strconv.Atoi(reservedValue)
		if err != nil {
			return 0, 0, fmt.Errorf("captureReservedConcurrency must be an integer: %w", err)
		}
	}
	if workerValue != "" {
		worker, err = strconv.Atoi(workerValue)
		if err != nil {
			return 0, 0, fmt.Errorf("captureWorkerMaxConcurrency must be an integer: %w", err)
		}
	}
	if reserved != -1 && reserved < 1 {
		return 0, 0, fmt.Errorf("captureReservedConcurrency must be -1 (shared account pool) or a positive integer; 0 disables invocations")
	}
	if worker < 2 || worker > 1000 {
		return 0, 0, fmt.Errorf("captureWorkerMaxConcurrency must be between 2 and 1000")
	}
	if reserved > 0 && worker > reserved {
		return 0, 0, fmt.Errorf("captureWorkerMaxConcurrency must not exceed captureReservedConcurrency")
	}
	return reserved, worker, nil
}

// Deployment tools provide a quota-checked override without rewriting stack YAML.
func effectiveCaptureReservation(requested int, override string) (int, error) {
	if override == "" {
		return requested, nil
	}
	value, err := strconv.Atoi(override)
	if err != nil || (value != -1 && value != requested) {
		return 0, fmt.Errorf("invalid quota-checked capture reservation override")
	}
	return value, nil
}
