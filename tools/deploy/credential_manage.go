package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"golang.org/x/text/unicode/norm"
)

func rotateVaultPassphrase(v *credentialVault, values savedCredentials, phrase string) (*credentialVault, error) {
	if norm.NFC.String(phrase) == norm.NFC.String(values.Pulumi) || norm.NFC.String(phrase) == norm.NFC.String(values.IndexPulumi) {
		return nil, errors.New("use a vault passphrase different from the Pulumi passphrases")
	}
	next, err := createVault(v.path, v.scope, phrase)
	if err != nil {
		return nil, err
	}
	if err = next.save(values); err != nil {
		clear(next.key)
		return nil, err
	}
	return next, nil
}
func manageCredentials(root string) error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return errors.New("credential management requires an interactive terminal")
	}
	environment, err := chooseEnvironment(localEnvironments(root))
	if err != nil {
		return err
	}
	repo := credentialRepository(root, environment, localRepository(root))
	if err := input("GitHub repository (credential vault scope)", &repo, false, true).Validate(func(value string) error {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(value) {
			return errors.New("use OWNER/REPOSITORY")
		}
		return nil
	}).Run(); err != nil {
		return err
	}

	path, scope, err := vaultLocation(repo, environment)
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
	var vault *credentialVault
	var values savedCredentials
	defer func() {
		if vault != nil {
			clear(vault.key)
		}
		values = savedCredentials{}
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
			if err := input("Current credential vault passphrase", &phrase, true, true).Run(); err != nil {
				return err
			}
			vault, values, err = unlockVault(path, scope, phrase)
			if err != nil {
				return err
			}
		}
		if action == "rotate" {
			phrase, repeat := "", ""
			if err := input("New vault passphrase (20+ characters; use random words)", &phrase, true, true).Validate(validateVaultPassphrase).Run(); err != nil {
				return err
			}
			if err := input("Confirm new vault passphrase", &repeat, true, true).Run(); err != nil {
				return err
			}
			if norm.NFC.String(phrase) != norm.NFC.String(repeat) {
				return errors.New("passphrases do not match; vault unchanged")
			}
			next, err := rotateVaultPassphrase(vault, values, phrase)
			if err != nil {
				return err
			}
			clear(vault.key)
			vault = next
		} else {
			updated := values
			switch action {
			case "github":
				err = input("New GitHub token", &updated.GitHub, true, true).Run()
			case "cloudflare":
				err = input("New Cloudflare API token (no Bearer prefix)", &updated.Cloudflare, true, true).Run()
			case "pulumi":
				err = input("Current application Pulumi passphrase to save", &updated.Pulumi, true, true).Run()
			case "index":
				err = input("Current shared-index Pulumi passphrase to save", &updated.IndexPulumi, true, true).Run()
			case "aws":
				updated.AWSAccess, updated.AWSSecret, updated.AWSToken = "", "", ""
				err = huh.NewForm(huh.NewGroup(input("AWS access key ID", &updated.AWSAccess, true, true), input("AWS secret access key", &updated.AWSSecret, true, true), input("AWS session token (required for temporary credentials)", &updated.AWSToken, true, false))).Run()
			}
			if err != nil {
				return err
			}
			if strings.HasPrefix(updated.AWSAccess, "ASIA") && updated.AWSToken == "" {
				return errors.New("temporary AWS credentials require a session token; vault unchanged")
			}
			if err = vault.save(updated); err != nil {
				return err
			}
			values = updated
		}
		fmt.Println("✓ Encrypted vault updated; no setup or cloud operations were run.")
	}
}

func credentialRepository(root, environment, fallback string) string {
	var saved struct{ Repository string }
	data, err := os.ReadFile(filepath.Join(root, "bootstrap."+environment+".local.json"))
	if err == nil && json.Unmarshal(data, &saved) == nil && regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(saved.Repository) {
		return saved.Repository
	}
	return fallback
}
