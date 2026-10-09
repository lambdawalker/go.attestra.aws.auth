package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

func setupDefaults(existing map[string]string) map[string]string {
	v := map[string]string{"AWS_ACCOUNT_ID": "", "AWS_ROLE_ARN": "", "AWS_REGION": "us-east-2", "PULUMI_BACKEND_URL": "s3://pulumi-state-1p8322nx", "PULUMI_STACK": "dev"}
	for key, value := range existing {
		v[key] = value
	}
	if v["AWS_ACCOUNT_ID"] == "" {
		parts := strings.Split(v["AWS_ROLE_ARN"], ":")
		if len(parts) == 6 {
			v["AWS_ACCOUNT_ID"] = parts[4]
		}
	}
	return v
}
func validateSetupValues(v map[string]string) error {
	if !regexp.MustCompile(`^[0-9]{12}$`).MatchString(v["AWS_ACCOUNT_ID"]) {
		return errors.New("AWS account ID must contain 12 digits")
	}
	prefix := "arn:aws:iam::" + v["AWS_ACCOUNT_ID"] + ":role/"
	if !strings.HasPrefix(v["AWS_ROLE_ARN"], prefix) || len(v["AWS_ROLE_ARN"]) == len(prefix) || strings.ContainsAny(v["AWS_ROLE_ARN"], " \r\n\t") {
		return errors.New("enter a deployment role ARN in the selected AWS account")
	}
	return (options{Backend: v["PULUMI_BACKEND_URL"], Stack: v["PULUMI_STACK"], Region: v["AWS_REGION"]}).validate()
}
func runGitHubSetup() error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("run GitHub setup in an interactive terminal")
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).Render("Attestra • GitHub environment setup"))
	fmt.Println("Creates/configures dev. Existing protections are preserved. Also configures AWS OIDC and a deployment role. Does not deploy the application.")
	fmt.Println("Token permissions: Administration write, Environments write, Actions read, and Metadata read for this repository.")
	repo, token := "lambdawalker/go.attestra.aws.auth", ""
	repoField := input("GitHub repository", &repo, false, true).Validate(func(v string) error {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(v) {
			return errors.New("use OWNER/REPOSITORY")
		}
		return nil
	})
	if err := huh.NewForm(huh.NewGroup(repoField, input("GitHub personal access token (hidden)", &token, true, true))).Run(); err != nil {
		return err
	}
	client := newGitHubClient(strings.TrimSpace(token))
	var user struct{ Login string }
	if _, err := client.request("GET", "/user", nil, &user); err != nil {
		return err
	}
	var metadata repositoryMetadata
	found, err := client.request("GET", "/repos/"+repo, nil, &metadata)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("repository not found or token lacks access")
	}
	fmt.Printf("Signed in as %s. Configuring %s / dev.\n", user.Login, repo)
	previous, err := client.inspectEnvironment(repo)
	if err != nil {
		return err
	}
	values := setupDefaults(previous.Variables)
	if previous.Exists {
		fmt.Println("Existing environment found. Press Enter to retain prefilled variable values.")
	}
	for _, name := range []string{"AWS_REGION", "PULUMI_BACKEND_URL", "PULUMI_STACK"} {
		value := values[name]
		if err := input(name, &value, false, true).Run(); err != nil {
			return err
		}
		values[name] = strings.TrimSpace(value)
	}
	if err := (options{Backend: values["PULUMI_BACKEND_URL"], Region: values["AWS_REGION"], Stack: values["PULUMI_STACK"]}).validate(); err != nil {
		return err
	}
	if err := setupAWSRole(client, repo, metadata, values); err != nil {
		return err
	}
	if err := validateSetupValues(values); err != nil {
		return err
	}
	passphrase := ""
	title := "Pulumi passphrase used during S3 migration (hidden)"
	if previous.SecretExists {
		title += "; leave blank to keep the existing secret"
	}
	if err := input(title, &passphrase, true, !previous.SecretExists).Run(); err != nil {
		return err
	}
	if passphrase != "" {
		repeat := ""
		if err := input("Repeat the Pulumi passphrase", &repeat, true, true).Run(); err != nil {
			return err
		}
		if repeat != passphrase {
			return errors.New("passphrases do not match; AWS setup changes were retained")
		}
	}
	fmt.Printf("\nRepository: %s\nEnvironment: dev\n", repo)
	for _, name := range environmentVariables {
		fmt.Printf("%s: %s\n", name, values[name])
	}
	if previous.Exists {
		fmt.Println("Existing environment protection rules: preserved.")
	} else {
		fmt.Println("New environment: restrict deployments to the main branch.")
	}
	if passphrase == "" {
		fmt.Println("Passphrase: keep existing secret.")
	} else {
		fmt.Println("Passphrase: store encrypted secret.")
	}
	if err := confirm("Save this GitHub environment configuration?"); err != nil {
		return err
	}
	saved, err := client.saveEnvironment(repo, previous, values, passphrase)
	if err != nil {
		if len(saved) > 0 {
			fmt.Println("Already saved:", strings.Join(saved, ", "))
		}
		return fmt.Errorf("setup stopped; completed changes were retained, rerun after fixing the error: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓ Environment configured and verified."))
	fmt.Printf("Review protections: https://github.com/%s/settings/environments\n", repo)
	fmt.Println("The token was not saved. AWS role configured. Finish S3 migration before running Deploy AWS (S3 state).")
	return nil
}
