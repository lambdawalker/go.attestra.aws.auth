package main

import (
	"errors"
	"strconv"

	"github.com/charmbracelet/huh"
)

func productionEnvironment(stack, class string) bool { return stack == "prod" || class == "production" }
func (w *bootstrapWizard) capacityDefaults(current map[string]struct{ Value string }) (map[string]string, error) {
	get := func(key string) string { return current["attestra-auth-email:"+key].Value }
	class := get("environmentClass")
	if class == "" {
		class = "test"
		if w.environment == "prod" {
			class = "production"
		} else if w.environment != "dev" && w.environment != "qa" {
			if err := huh.NewSelect[string]().Title("Capacity profile for "+w.environment).Options(huh.NewOption("Test — shared concurrency, two worker invocations", "test"), huh.NewOption("Production — explicit capacity and strict quota check", "production")).Value(&class).Run(); err != nil {
				return nil, err
			}
		}
	}
	if class != "test" && class != "production" || w.environment == "prod" && class != "production" {
		return nil, errors.New("environmentClass must be test or production; prod requires production")
	}
	reserved, workers := get("captureReservedConcurrency"), get("captureWorkerMaxConcurrency")
	if productionEnvironment(w.environment, class) {
		positive := func(s string) error {
			n, e := strconv.Atoi(s)
			if e != nil || n < 2 {
				return errors.New("enter an integer of at least 2")
			}
			return nil
		}
		if reserved == "" {
			if err := input("Reserved concurrency per capture Lambda (three functions total)", &reserved, false, true).Validate(positive).Run(); err != nil {
				return nil, err
			}
		}
		if workers == "" {
			if err := input("Maximum concurrent SQS worker invocations (2–1000, no more than reservation)", &workers, false, true).Validate(positive).Run(); err != nil {
				return nil, err
			}
		}
	} else {
		if reserved == "" {
			reserved = "-1"
		}
		if workers == "" {
			workers = "2"
		}
	}
	r, e := strconv.Atoi(reserved)
	n, e2 := strconv.Atoi(workers)
	if e != nil || e2 != nil || (r != -1 && r < 1) || n < 2 || n > 1000 || (r > 0 && n > r) || (class == "production" && r < 1) {
		return nil, errors.New("invalid capacity configuration; production requires reservations, workers must be 2–1000 and not exceed reserved concurrency")
	}
	return map[string]string{"attestra-auth-email:environmentClass": class, "attestra-auth-email:captureReservedConcurrency": reserved, "attestra-auth-email:captureWorkerMaxConcurrency": workers}, nil
}
