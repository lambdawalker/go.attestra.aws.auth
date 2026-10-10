package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"

	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"golang.org/x/text/unicode/norm"
)

func manageCredentials(root string) error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("credential management requires an interactive terminal")
	}
	environment, err := ui.ChooseEnvironment(localEnvironments(root))
	if err != nil {
		return err
	}
	repo := credentialRepository(root, environment, localRepository(root))
	if err := ui.Input("GitHub repository (credential vault scope)", &repo, false, true).Validate(func(value string) error {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(value) {
			return errors.New("use OWNER/REPOSITORY")
		}
		return nil
	}).Run(); err != nil {
		return err
	}

	path, scope, err := credentialvault.Location(repo, environment)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); os.IsNotExist(err) {
		fmt.Println("No saved credential vault for", repo, environment)
		return nil
	} else if err != nil {
		return err
	}
	fmt.Printf("Local credential vault • %s • %s\nChanges affect only this encrypted file. They do not rotate provider tokens or Pulumi stack encryption. AWS CLI login caches are separate.\n", repo, environment)
	var vault *credentialvault.Vault
	var values credentialvault.Credentials
	defer func() {
		if vault != nil {
			vault.Close()
		}
		values = credentialvault.Credentials{}
	}()
	for {
		action := "exit"
		if err := huh.NewSelect[string]().Title("Manage saved credentials").Options(
			huh.NewOption("Replace GitHub token", "github"), huh.NewOption("Replace Cloudflare token", "cloudflare"), huh.NewOption("Replace manual AWS credentials", "aws"),
			huh.NewOption("Replace saved application Pulumi passphrase", "pulumi"), huh.NewOption("Replace saved shared-index Pulumi passphrase", "index"),
			huh.NewOption("Change vault passphrase", "rotate"), huh.NewOption("Delete all saved credentials for this environment", "delete"), huh.NewOption("Finish", "exit")).Value(&action).Run(); err != nil {
			return err
		}
		if action == "exit" {
			return nil
		}
		if action == "delete" {
			if err := typedConfirmation("Delete the local vault (tokens are not revoked); type the environment", environment); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			fmt.Println("✓ Local vault deleted. Provider credentials and AWS login caches remain unchanged.")
			return nil
		}
		if vault == nil {
			phrase := ""
			if err := ui.Input("Current credential vault passphrase", &phrase, true, true).Run(); err != nil {
				return err
			}
			vault, values, err = credentialvault.Unlock(path, scope, phrase)
			if err != nil {
				return err
			}
		}
		if action == "rotate" {
			phrase, repeat := "", ""
			if err := ui.Input("New vault passphrase (20+ characters; use random words)", &phrase, true, true).Validate(credentialvault.ValidatePassphrase).Run(); err != nil {
				return err
			}
			if err := ui.Input("Confirm new vault passphrase", &repeat, true, true).Run(); err != nil {
				return err
			}
			if norm.NFC.String(phrase) != norm.NFC.String(repeat) {
				return errors.New("passphrases do not match; vault unchanged")
			}
			next, err := credentialvault.RotatePassphrase(vault, values, phrase)
			if err != nil {
				return err
			}
			vault.Close()
			vault = next
		} else {
			updated := values
			switch action {
			case "github":
				err = ui.Input("New GitHub token", &updated.GitHub, true, true).Run()
			case "cloudflare":
				err = ui.Input("New Cloudflare API token (no Bearer prefix)", &updated.Cloudflare, true, true).Run()
			case "pulumi":
				err = ui.Input("Current application Pulumi passphrase to save", &updated.Pulumi, true, true).Run()
			case "index":
				err = ui.Input("Current shared-index Pulumi passphrase to save", &updated.IndexPulumi, true, true).Run()
			case "aws":
				updated.AWSAccess, updated.AWSSecret, updated.AWSToken = "", "", ""
				err = huh.NewForm(huh.NewGroup(ui.Input("AWS access key ID", &updated.AWSAccess, true, true), ui.Input("AWS secret access key", &updated.AWSSecret, true, true), ui.Input("AWS session token (required for temporary credentials)", &updated.AWSToken, true, false))).Run()
			}
			if err != nil {
				return err
			}
			if strings.HasPrefix(updated.AWSAccess, "ASIA") && updated.AWSToken == "" {
				return errors.New("temporary AWS credentials require a session token; vault unchanged")
			}
			if err = vault.Save(updated); err != nil {
				return err
			}
			values = updated
		}
		fmt.Println("✓ Encrypted vault updated; no setup or cloud operations were run.")
	}
}

func credentialRepository(root, environment, fallback string) string {
	var saved struct{ Repository string }
	data, err := os.ReadFile(filepath.Join(root, ".attestra", "bootstrap."+environment+".local.json"))
	if err == nil && json.Unmarshal(data, &saved) == nil && regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(saved.Repository) {
		return saved.Repository
	}
	return fallback
}
