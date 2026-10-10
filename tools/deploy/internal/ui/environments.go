package ui

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
)

// One DNS-safe name is used for the GitHub environment and Pulumi stack.
func ValidateEnvironment(name string) error {
	if len(name) > 32 || !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(name) {
		return errors.New("use 1–32 lowercase letters, digits or single hyphens; start with a letter and end with a letter/digit")
	}
	return nil
}
func ChooseEnvironment(existing []string) (string, error) {
	return ChooseEnvironmentFor(existing, "Environment to set up")
}
func ChooseEnvironmentFor(existing []string, title string) (string, error) {
	names := []string{"dev", "qa", "prod"}
	for _, name := range existing {
		if ValidateEnvironment(name) != nil {
			fmt.Printf("Environment %q cannot be managed by this wizard: use a lowercase DNS-safe name.\n", name)
			continue
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	choices := make([]huh.Option[string], 0, len(names)+1)
	for _, name := range names {
		label := name
		if slices.Contains(existing, name) {
			label += " (local configuration)"
		}
		choices = append(choices, huh.NewOption(label, name))
	}
	choices = append(choices, huh.NewOption("Use another environment…", ""))
	selected := "dev"
	if err := EnvironmentSelect(choices, &selected).Title(title).Run(); err != nil {
		return "", err
	}
	if selected == "" {
		namePrompt := "Environment name (existing or new, for example staging or demo-2)"
		if title == "Environment to tear down" {
			namePrompt = "Existing environment name to tear down"
		}
		if err := Input(namePrompt, &selected, false, true).Validate(ValidateEnvironment).Run(); err != nil {
			return "", err
		}
	}
	// GitHub environment names are case-insensitive. Do not create an alias of an
	// existing name whose casing cannot be used in stack/DNS/OIDC configuration.
	for _, name := range existing {
		if strings.EqualFold(name, selected) && name != selected {
			return "", fmt.Errorf("existing environment %q differs in casing; rename it deliberately before setup", name)
		}
	}
	return selected, ValidateEnvironment(selected)
}

// Bind the default before Options: Huh otherwise selects the empty-valued
// custom option and keeps its viewport at the bottom even after Value changes.
func EnvironmentSelect(choices []huh.Option[string], selected *string) *huh.Select[string] {
	return huh.NewSelect[string]().Title("Environment to set up").Description(fmt.Sprintf("%d choices • ↑/↓ to browse/scroll • Enter to select. Local choices only; use another environment to enter any name.", len(choices))).Value(selected).Options(choices...)
}
