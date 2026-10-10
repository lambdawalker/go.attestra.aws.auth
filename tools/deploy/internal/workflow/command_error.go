package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Captured stdout is deliberately excluded: exports and config reads may
// contain secrets. Config writes suppress stderr too, since providers may echo
// submitted values. Read diagnostics redact known credentials before display.
func pulumiCommandError(args []string, diagnostic string, env []string, cause error) error {
	operation := "pulumi"
	if len(args) > 0 {
		switch args[0] {
		case "stack", "config":
			operation += " " + args[0]
			if len(args) > 1 {
				switch args[1] {
				case "ls", "select", "export", "output", "rm", "init", "set", "get", "refresh":
					operation += " " + args[1]
				}
			}
		case "login", "preview", "up", "destroy", "version":
			operation += " " + args[0]
		}
	}
	if len(args) > 1 && args[0] == "config" && args[1] == "set" {
		return fmt.Errorf("%s failed: %w (configuration-write diagnostics suppressed to protect secret values)", operation, cause)
	}
	// Remove terminal escape sequences before matching redaction values.
	clean := func(text string) string {
		text = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`).ReplaceAllString(text, "")
		text = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, text)
		return text
	}
	diagnostic = clean(diagnostic)
	var secrets []string
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		key = strings.ToUpper(key)
		for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSPHRASE", "ACCESS_KEY", "CREDENTIAL_SOURCE"} {
			if strings.Contains(key, marker) {
				secrets = append(secrets, value)
				if normalized := clean(value); normalized != "" {
					secrets = append(secrets, normalized)
				}
				encoded, _ := json.Marshal(value)
				if len(encoded) > 2 {
					secrets = append(secrets, string(encoded[1:len(encoded)-1]))
				}
				break
			}
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		diagnostic = strings.ReplaceAll(diagnostic, secret, "[redacted]")
	}
	diagnostic = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`).ReplaceAllString(diagnostic, "Bearer [redacted]")
	diagnostic = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`).ReplaceAllString(diagnostic, "[redacted AWS access key]")
	diagnostic = regexp.MustCompile(`https?://[^\s?]+\?[^\s]+`).ReplaceAllString(diagnostic, "[URL query redacted]")
	diagnostic = strings.TrimSpace(diagnostic)
	if len(diagnostic) > 4096 {
		diagnostic = diagnostic[:4096] + "\n[diagnostic truncated]"
	}
	if diagnostic == "" {
		return fmt.Errorf("%s failed: %w (no diagnostic text returned)", operation, cause)
	}
	return fmt.Errorf("%s failed: %w\n%s", operation, cause, diagnostic)
}
