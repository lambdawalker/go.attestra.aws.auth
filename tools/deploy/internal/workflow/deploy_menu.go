package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

// Launcher bookkeeping does not count as a user-selected operation.
// A completion record routes the UI only; deployment still verifies live state.
func deploymentStartup(root, repo string, flags []string, choose func() (string, error)) (string, error) {
	for _, name := range flags {
		if name != "repo-root" && name != "bootstrap" {
			return "explicit", nil
		}
	}
	paths, err := statePaths(filepath.Join(root, ".attestra"), "bootstrap.*.local.json", "bootstrap.*.local.json.bak")
	if err != nil {
		return "", err
	}
	// Include dev even when only an unqualified legacy checkpoint exists.
	environments := []string{"dev"}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".bak")
		environment := strings.TrimSuffix(strings.TrimPrefix(name, "bootstrap."), ".local.json")
		if environment != "" && !slices.Contains(environments, environment) {
			environments = append(environments, environment)
		}
	}
	completed := false
	for _, environment := range environments {
		checkpoint, err := readBootstrapCheckpoint(root, environment)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if checkpoint.belongsTo(repo, environment) && (checkpoint.Environment != "" || environment == "dev") && checkpoint.Stage == "complete" {
			completed = true
		}
	}
	if !completed {
		return "setup", nil
	}
	return choose()
}

func chooseDeployment() (string, error) {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return "", errors.New("run in an interactive terminal to choose a deployment, or specify an explicit operation")
	}
	choice := "app"
	err := huh.NewSelect[string]().Title("What would you like to deploy?").Value(&choice).Options(
		huh.NewOption("Deploy both (index/admin, then backend)", "both"),
		huh.NewOption("Deploy the index and admin only", "shared"),
		huh.NewOption("Deploy the backend only (default stack: dev)", "app"),
	).Run()
	return choice, err
}

func runDeploymentChoice(scope string, shared, backend func() error) error {
	switch scope {
	case "shared":
		return shared()
	case "app":
		return backend()
	case "both":
		if err := shared(); err != nil {
			return fmt.Errorf("shared deployment did not complete; backend deployment was not started: %w", err)
		}
		return backend()
	default:
		return errors.New("unknown deployment choice")
	}
}
