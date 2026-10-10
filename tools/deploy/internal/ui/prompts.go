package ui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/huh"
)

func Required(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("Required")
	}
	return nil
}
func Input(title string, value *string, secret, mandatory bool) *huh.Input {
	field := huh.NewInput().Title(title).Value(value)
	if secret {
		field.EchoMode(huh.EchoModePassword)
	}
	if mandatory {
		field.Validate(Required)
	}
	return field
}
func Confirm(title string) error {
	yes := false
	if err := huh.NewConfirm().Title(title).Affirmative("Continue").Negative("Cancel").Value(&yes).Run(); err != nil {
		return err
	}
	if !yes {
		return errors.New("cancelled")
	}
	return nil
}
