package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func TestEnvironmentMenuInitiallyShowsAllDefaultOptions(t *testing.T) {
	selected := "dev"
	menu := EnvironmentSelect([]huh.Option[string]{huh.NewOption("dev", "dev"), huh.NewOption("qa", "qa"), huh.NewOption("prod", "prod"), huh.NewOption("Use another environment…", "")}, &selected)
	view := menu.View()
	for _, label := range []string{"dev", "qa", "prod", "Use another environment…"} {
		if !strings.Contains(view, label) {
			t.Fatalf("initial menu hides %q:\n%s", label, view)
		}
	}
	if selected != "dev" {
		t.Fatalf("default changed to %q", selected)
	}
}
