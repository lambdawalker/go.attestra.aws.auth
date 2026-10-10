//go:build !windows

package workflow

import "errors"

func shortCredentialPath(string) (string, error) {
	return "", errors.New("Windows short paths are unavailable on this platform")
}
