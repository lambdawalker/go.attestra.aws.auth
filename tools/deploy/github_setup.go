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

func setupDefaults(environment string, existing map[string]string) map[string]string {
	v := map[string]string{"AWS_ACCOUNT_ID": "", "AWS_ROLE_ARN": "", "AWS_REGION": "us-east-2", "PULUMI_BACKEND_URL": freshBackend(), "PULUMI_STACK": environment}
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
func runGitHubSetup(root string, full, force bool) error {
	var bootstrap *bootstrapWizard
	if full {
		bootstrap = &bootstrapWizard{root: root}
		defer bootstrap.cleanup()
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("run GitHub setup in an interactive terminal")
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).Render("Attestra • GitHub environment setup"))
	if full {
		fmt.Println("Full bootstrap: state bucket → stack and secrets → IAM/GitHub → deploy and SES DNS guidance. Existing resources are preserved.")
	}
	environment, err := chooseEnvironment(localEnvironments(root))
	if err != nil {
		return err
	}
	repo := localRepository(root)
	if repo == "" {
		repo = "lambdawalker/go.attestra.aws.auth"
	}
	if bootstrap != nil {
		bootstrap.environment = environment
		if err := bootstrap.loadSelections(repo, map[string]string{}, nil); err != nil {
			return err
		}
		skip, err := bootstrap.checkLocalSetup(repo, force, confirm)
		if err != nil || skip {
			return err
		}
		if err := bootstrap.preflight(); err != nil {
			return err
		}
		// Invalidate the old completion before any remote configuration can change.
		if err := bootstrap.markStage("setup-in-progress"); err != nil {
			return err
		}
	}
	fmt.Println("Creates/configures the selected environment. Existing protections are preserved. Also configures AWS OIDC and a deployment role.")
	fmt.Println("Token permissions: Administration write, Environments write, Actions read, and Metadata read for this repository.")
	token := ""
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
	names, err := client.listEnvironments(repo)
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.EqualFold(name, environment) && name != environment {
			return fmt.Errorf("existing environment %q differs in casing; rename it deliberately before setup", name)
		}
	}
	if bootstrap != nil && bootstrap.memory.Repository != repo {
		bootstrap.memory = bootstrapCheckpoint{}
		bootstrap.resuming = false
	}
	fmt.Printf("Signed in as %s. Configuring %s / %s.\n", user.Login, repo, environment)
	previous, err := client.inspectEnvironment(repo, environment)
	if err != nil {
		return err
	}
	values := setupDefaults(environment, previous.Variables)
	if full {
		if err := bootstrap.loadSelections(repo, values, previous.Variables); err != nil {
			return err
		}
	}
	if previous.Exists {
		fmt.Println("Existing environment found. Press Enter to retain prefilled variable values.")
	}
	for _, name := range []string{"AWS_REGION", "PULUMI_BACKEND_URL"} {
		value := values[name]
		if full && (previous.Variables[name] != "" || bootstrap.resuming) {
			fmt.Printf("Using %s: %s\n", name, value)
			continue
		}
		if err := input(name, &value, false, true).Run(); err != nil {
			return err
		}
		values[name] = strings.TrimSpace(value)
	}
	if err := (options{Backend: values["PULUMI_BACKEND_URL"], Region: values["AWS_REGION"], Stack: values["PULUMI_STACK"]}).validate(); err != nil {
		return err
	}
	if values["PULUMI_STACK"] != environment {
		return errors.New("PULUMI_STACK must match the selected environment; inspect its existing configuration before resuming")
	}
	if full {
		if err := bootstrap.saveSelections(repo, values); err != nil {
			return err
		}
	}
	if err := setupAWSRole(client, repo, environment, metadata, values, bootstrap); err != nil {
		return err
	}
	if err := validateSetupValues(values); err != nil {
		return err
	}
	passphrase := ""
	if bootstrap != nil {
		passphrase = bootstrap.passphrase
	}
	title := "Existing S3 stack passphrase (hidden)"
	if previous.SecretExists {
		title += "; leave blank to keep the existing secret"
	}
	if bootstrap == nil {
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
	}
	fmt.Printf("\nRepository: %s\nEnvironment: %s\n", repo, environment)
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
	if !full {
		if err := confirm("Save this GitHub environment configuration?"); err != nil {
			return err
		}
	}
	saved, err := client.saveEnvironment(repo, environment, previous, values, passphrase)
	if err != nil {
		if len(saved) > 0 {
			fmt.Println("Already saved:", strings.Join(saved, ", "))
		}
		return fmt.Errorf("setup stopped; completed changes were retained, rerun after fixing the error: %w", err)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓ Environment configured and verified."))
	fmt.Printf("Review protections: https://github.com/%s/settings/environments\n", repo)
	fmt.Println("The token was not saved. AWS role configured.")
	if bootstrap != nil {
		if err := bootstrap.saveSelections(repo, values); err != nil {
			return err
		}
		if err := bootstrap.markStage("github-ready"); err != nil {
			return err
		}
		return bootstrap.finish(repo)
	}
	fmt.Println("Ensure the S3 stack is initialized or migrated before running Deploy AWS [Pulumi S3].")
	return nil
}
