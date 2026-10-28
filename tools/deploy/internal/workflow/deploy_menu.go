package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	paths, err := filepath.Glob(filepath.Join(root, ".attestra", "bootstrap.*.local.json"))
	if err != nil {
		return "", err
	}
	paths = append(paths, filepath.Join(root, ".attestra", "bootstrap.local.json"))
	completed := false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		var checkpoint bootstrapCheckpoint
		if json.Unmarshal(data, &checkpoint) != nil {
			return "", fmt.Errorf("invalid setup record %s; inspect it before continuing", path)
		}
		// Incomplete or foreign records cannot establish that setup has finished.
		if checkpoint.Repository == repo && checkpoint.Stack != "" && (checkpoint.Environment == checkpoint.Stack || checkpoint.Environment == "" && checkpoint.Stack == "dev") && checkpoint.Stage == "complete" {
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
