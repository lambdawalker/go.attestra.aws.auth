package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

// Deployment preferences contain only explicitly allowlisted non-secret values.
// Keep these separate from bootstrap progress: deploying must not mark setup done.
type deploySettings struct {
	Repository string
	Stack      string
	Backend    string
	Region     string
	AuthMode   string
	Profile    string
}

func readDeploySettings(root, stack, repo string) (deploySettings, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(stack) {
		return deploySettings{}, errors.New("use a simple environment/stack name")
	}
	var result deploySettings
	path := filepath.Join(root, ".attestra", "deploy."+stack+".local.json")
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &result); err != nil {
			return result, fmt.Errorf("invalid deployment settings in %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return result, err
	} else {
		path = filepath.Join(root, ".attestra", "bootstrap."+stack+".local.json")
		data, err = os.ReadFile(path)
		if os.IsNotExist(err) {
			data, err = os.ReadFile(path + ".bak")
		}
		if os.IsNotExist(err) && stack == "dev" {
			data, err = os.ReadFile(filepath.Join(root, ".attestra", "bootstrap.local.json"))
		}
		if os.IsNotExist(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		var checkpoint bootstrapCheckpoint
		if err := json.Unmarshal(data, &checkpoint); err != nil {
			return result, fmt.Errorf("invalid bootstrap settings: %w", err)
		}
		result = deploySettings{Repository: checkpoint.Repository, Stack: checkpoint.Stack, Backend: checkpoint.Backend, Region: checkpoint.Region, AuthMode: checkpoint.Settings["authMode"], Profile: checkpoint.Settings["profile"]}
	}
	switch result.AuthMode {
	case "", "sso", "login", "keys":
	default:
		return deploySettings{}, errors.New("invalid saved AWS authentication method; inspect the local settings")
	}
	if result.Stack != stack || result.Repository != repo {
		return deploySettings{}, errors.New("saved deployment settings belong to a different repository or environment; inspect the local settings before continuing")
	}
	return result, nil
}

func applyDeploySettings(o *options, saved deploySettings, explicit map[string]bool) {
	if !explicit["backend"] && saved.Backend != "" {
		o.Backend = saved.Backend
	}
	if !explicit["region"] && saved.Region != "" {
		o.Region = saved.Region
	}
	if !explicit["profile"] && saved.Profile != "" {
		o.Profile = saved.Profile
	}
	if !explicit["login"] && !explicit["sso"] {
		o.Login, o.Sso = saved.AuthMode == "login", saved.AuthMode == "sso"
	}
}

func saveDeploySettings(o options, repo string) error {
	mode := "keys"
	if o.Login {
		mode = "login"
	}
	if o.Sso {
		mode = "sso"
	}
	data, err := json.MarshalIndent(deploySettings{Repository: repo, Stack: o.Stack, Backend: o.Backend, Region: o.Region, AuthMode: mode, Profile: o.Profile}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(o.Root, ".attestra")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "deploy-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "deploy."+o.Stack+".local.json"))
}

func configureLocalDeploy(o *options, explicit map[string]bool) (*bootstrapWizard, error) {
	if !explicit["stack"] {
		if err := ui.Input("Environment / stack", &o.Stack, false, true).Run(); err != nil {
			return nil, err
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(o.Stack) {
		return nil, errors.New("use a simple environment/stack name")
	}
	repo := credentialRepository(o.Root, o.Stack, localRepository(o.Root))
	if repo == "" {
		return nil, errors.New("cannot determine repository for saved settings and vault; configure a GitHub origin remote")
	}
	settings, err := readDeploySettings(o.Root, o.Stack, repo)
	if err != nil {
		return nil, err
	}
	applyDeploySettings(o, settings, explicit)
	if o.Backend == "" {
		if err := ui.Input("S3 state URL (bucket must already exist)", &o.Backend, false, true).Run(); err != nil {
			return nil, err
		}
	}
	if !explicit["region"] && settings.Region == "" {
		if err := ui.Input("AWS region", &o.Region, false, true).Run(); err != nil {
			return nil, err
		}
	}
	if !explicit["login"] && !explicit["sso"] && settings.AuthMode == "" {
		mode := "sso"
		if err := huh.NewSelect[string]().Title("AWS authentication").Options(huh.NewOption("SSO", "sso"), huh.NewOption("AWS browser login", "login"), huh.NewOption("Access key credentials", "keys")).Value(&mode).Run(); err != nil {
			return nil, err
		}
		o.Sso, o.Login = mode == "sso", mode == "login"
	}
	if (o.Login || o.Sso) && !explicit["profile"] && settings.Profile == "" {
		if err := ui.Input("AWS login profile", &o.Profile, false, true).Run(); err != nil {
			return nil, err
		}
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	fmt.Printf("Using stack %s • region %s • state %s\n", o.Stack, o.Region, o.Backend)
	w := &bootstrapWizard{root: o.Root, environment: o.Stack}
	// Reuse the exact repository/environment vault used by setup. Do not load DNS
	// selections here: deploying does not need to request Cloudflare/GitHub tokens.
	if err := w.loadCredentialsPrompt(repo, false, false); err != nil {
		return nil, err
	}
	return w, nil
}

func deployCredentials(w *bootstrapWizard, o options) (credentials, error) {
	c := credentials{Access: w.secrets.AWSAccess, Secret: w.secrets.AWSSecret, Token: w.secrets.AWSToken}
	if o.Login || o.Sso {
		var err error
		c, err = loginCredentials(&processRunner{env: loginEnvironment(os.Environ(), o.Region)}, o)
		if err != nil {
			return c, err
		}
	} else {
		if c.Access == "" {
			if err := ui.Input("AWS access key ID", &c.Access, true, true).Run(); err != nil {
				return c, err
			}
		}
		if c.Secret == "" {
			if err := ui.Input("AWS secret access key", &c.Secret, true, true).Run(); err != nil {
				return c, err
			}
		}
		c.Access, c.Secret = strings.TrimSpace(c.Access), strings.TrimSpace(c.Secret)
		if strings.HasPrefix(c.Access, "ASIA") && c.Token == "" {
			if err := ui.Input("AWS session token (required for temporary credentials)", &c.Token, true, true).Run(); err != nil {
				return c, err
			}
		}
		c.Token = strings.TrimSpace(c.Token)
		if strings.HasPrefix(c.Access, "ASIA") && c.Token == "" {
			return c, errors.New("temporary AWS access keys require the session token")
		}
	}
	if o.MigrateFrom == "" {
		c.Passphrase = w.secrets.Pulumi
	}
	if c.Passphrase == "" {
		if err := ui.Input("Pulumi state passphrase (keep a recoverable copy)", &c.Passphrase, true, true).Run(); err != nil {
			return c, err
		}
	}
	return c, nil
}
