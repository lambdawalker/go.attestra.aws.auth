package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
)

// One DNS-safe name is used for the GitHub environment and Pulumi stack.
func validateEnvironment(name string) error {
	if len(name) > 32 || !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(name) {
		return errors.New("use 1–32 lowercase letters, digits or single hyphens; start with a letter and end with a letter/digit")
	}
	return nil
}
func applicationDefaults(environment, base string) (map[string]string, error) {
	if err := validateEnvironment(environment); err != nil {
		return nil, err
	}
	label := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	parts := strings.Split(base, ".")
	if len(parts) < 2 || len(environment+".info."+base) > 253 {
		return nil, errors.New("enter a base domain such as example.com, without a scheme or path")
	}
	for _, p := range parts {
		if len(p) > 63 || !label.MatchString(p) {
			return nil, errors.New("invalid base domain; use lowercase DNS labels")
		}
	}
	if !regexp.MustCompile(`^[a-z]{2,}$`).MatchString(parts[len(parts)-1]) {
		return nil, errors.New("base domain must have a DNS top-level domain")
	}
	sender := environment + ".info." + base
	return map[string]string{
		"attestra-auth-email:appOrigin":     "https://" + environment + "." + base,
		"attestra-auth-email:senderDomain":  sender,
		"attestra-auth-email:senderAddress": "verify@" + sender,
	}, nil
}
func (g *githubClient) listEnvironments(repo string) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		var response struct{ Environments []struct{ Name string } }
		found, err := g.request("GET", fmt.Sprintf("/repos/%s/environments?per_page=100&page=%d", repo, page), nil, &response)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("cannot list repository environments; check token permissions")
		}
		for _, env := range response.Environments {
			names = append(names, env.Name)
		}
		if len(response.Environments) < 100 {
			return names, nil
		}
	}
}
func chooseEnvironment(existing []string) (string, error) {
	names := []string{"dev", "qa", "prod"}
	for _, name := range existing {
		if validateEnvironment(name) != nil {
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
			label += " (existing)"
		} else {
			label += " (new)"
		}
		choices = append(choices, huh.NewOption(label, name))
	}
	choices = append(choices, huh.NewOption("Create another environment…", ""))
	selected := "dev"
	if err := huh.NewSelect[string]().Title("Environment to set up").Description("Choose dev, qa, prod, or an existing environment. Select Create another environment to enter a custom name.").Height(len(choices)).Options(choices...).Value(&selected).Run(); err != nil {
		return "", err
	}
	if selected == "" {
		if err := input("New environment name (for example staging or demo-2)", &selected, false, true).Validate(validateEnvironment).Run(); err != nil {
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
	return selected, validateEnvironment(selected)
}
