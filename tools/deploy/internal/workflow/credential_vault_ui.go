package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
	credentialvault "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/vault"

	"github.com/charmbracelet/huh"
	"golang.org/x/text/unicode/norm"
)

func (w *bootstrapWizard) loadCredentials(repo string, fresh bool) error {
	w.credentialRepo = repo
	path, scope, err := credentialvault.Location(repo, w.environment)
	if err != nil {
		return err
	}
	if fresh {
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	use := true
	if err := huh.NewConfirm().Title("Unlock saved credentials for " + w.environment + "?").Description("No uses credentials entered for this run. The encrypted vault stays unchanged unless you choose to replace it later.").Value(&use).Run(); err != nil {
		return err
	}
	if !use {
		return nil
	}
	phrase := ""
	if err := ui.Input("Credential vault passphrase", &phrase, true, true).Run(); err != nil {
		return err
	}
	vault, values, err := credentialvault.Unlock(path, scope, phrase)
	if err != nil {
		return fmt.Errorf("%w; rerun and decline vault unlock to enter fresh credentials, or use credentials.bat/credentials.sh to manage the vault", err)
	}
	w.vault, w.secrets = vault, values
	fmt.Println("✓ Credentials unlocked locally. Saved tokens may expire; use credentials.bat/credentials.sh to replace them. AWS SSO/browser sessions remain managed by the AWS CLI.")
	return nil
}
func (w *bootstrapWizard) ensureCloudflare() error {
	if w.cf != nil {
		return nil
	}
	if w.secrets.Cloudflare == "" {
		fmt.Println("Cloudflare token needs Zone Read and DNS Edit for the authoritative zone.")
		if err := ui.Input("Cloudflare API token (hidden)", &w.secrets.Cloudflare, true, true).Run(); err != nil {
			return err
		}
		w.secrets.Cloudflare = strings.TrimSpace(w.secrets.Cloudflare)
	}
	w.cf = dns.NewClient(w.secrets.Cloudflare)
	return nil
}
func (w *bootstrapWizard) offerCredentialSaving() error {
	if w.memory.Settings["dnsProvider"] == "cloudflare" {
		if err := w.ensureCloudflare(); err != nil {
			return err
		}
	}
	w.secrets.Pulumi = w.passphrase
	if w.vault != nil {
		return w.vault.Save(w.secrets)
	}
	save := false
	if err := huh.NewConfirm().Title("Save these credentials in an encrypted local vault?").Description("Includes GitHub, Cloudflare (if used), the application and shared index Pulumi passphrases, and manually entered AWS keys/session token. AWS expiry still applies. Default: do not save.").Value(&save).Run(); err != nil {
		return err
	}
	if !save {
		return nil
	}
	path, scope, err := credentialvault.Location(w.credentialRepo, w.environment)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := ui.Confirm("Replace the existing encrypted credential vault for " + w.environment + "?"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Use a separate passphrase: at least 20 characters, ideally 5+ random words or a password-manager-generated secret. Spaces and Unicode are allowed. Keep it in your password manager; it cannot be recovered.")
	phrase, repeat := "", ""
	if err := ui.Input("New credential vault passphrase", &phrase, true, true).Validate(func(s string) error {
		if err := credentialvault.ValidatePassphrase(s); err != nil {
			return err
		}
		if norm.NFC.String(s) == norm.NFC.String(w.passphrase) || norm.NFC.String(s) == norm.NFC.String(w.secrets.IndexPulumi) {
			return errors.New("use a different passphrase from the Pulumi stack passphrase")
		}
		return nil
	}).Run(); err != nil {
		return err
	}
	if err := ui.Input("Confirm credential vault passphrase", &repeat, true, true).Run(); err != nil {
		return err
	}
	if norm.NFC.String(phrase) != norm.NFC.String(repeat) {
		return errors.New("vault passphrases do not match; credentials were not saved")
	}
	vault, err := credentialvault.Create(path, scope, phrase)
	if err != nil {
		return err
	}
	if err = vault.Save(w.secrets); err != nil {
		vault.Close()
		return err
	}
	w.vault = vault
	fmt.Println("✓ Encrypted credentials saved outside the repository:", path)
	return nil
}
