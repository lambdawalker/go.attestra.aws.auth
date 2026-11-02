package workflow

import "fmt"

func validateNukeFlags(nuke, teardown bool, supplied []string) error {
	for _, name := range supplied {
		if name == "check" && (!nuke || !teardown) {
			return fmt.Errorf("-check requires -teardown -nuke")
		}
	}
	if !nuke {
		return nil
	}
	if !teardown {
		return fmt.Errorf("-nuke requires -teardown")
	}
	for _, name := range supplied {
		if name != "nuke" && name != "teardown" && name != "repo-root" && name != "check" {
			return fmt.Errorf("-%s cannot be combined with -teardown -nuke", name)
		}
	}
	return nil
}

// Nuke uses an explicit success allowlist: new/unknown statuses must never
// accidentally authorize deleting the only remaining recovery data.
type nukeResult struct{ Name, Status, Detail string }

func nukeSuccessful(results []nukeResult) bool {
	for _, r := range results {
		switch r.Status {
		case "DELETED", "ABSENT", "SCHEDULED", "INFO":
		default:
			return false
		}
	}
	return true
}

func nukeRecovery(results []nukeResult, cleanup func()) {
	if nukeSuccessful(results) {
		cleanup()
	}
}

// exists must return false only for authoritative absence, never for denial,
// network errors or an incomplete response. A successful DELETE is insufficient.
func nukeStep(name string, exists func() (bool, error), remove func() error) nukeResult {
	found, err := exists()
	if err != nil {
		return nukeResult{name, "FAILED", err.Error()}
	}
	if !found {
		return nukeResult{name, "ABSENT", "already absent; confirmed by read-back"}
	}
	if err = remove(); err != nil {
		// A concurrent/retried delete can return not-found: the read-back below
		// remains the authority. Preserve the actual error if the read fails.
		if present, checkErr := exists(); checkErr == nil && !present {
			return nukeResult{name, "DELETED", "absence verified after delete"}
		}
		return nukeResult{name, "FAILED", err.Error()}
	}
	found, err = exists()
	if err != nil {
		return nukeResult{name, "UNVERIFIED", err.Error()}
	}
	if found {
		return nukeResult{name, "REMAINS", "still present after delete; retry cleanup"}
	}
	return nukeResult{name, "DELETED", "absence verified after delete"}
}
