package aws

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type Client struct {
	Env            []string
	RenewalFailure func([]string, string) error
}

type Error struct{ Operation, Code string }

func (e *Error) Error() string {
	return "AWS " + e.Operation + ": " + e.Code + " (check permissions/session; completed changes retained)"
}

type API interface {
	Call(result any, args ...string) (bool, error)
}

// Credentials stay in the isolated child environment. Only the AWS error code
// is surfaced; CLI output can contain credentials or sensitive policy values.
func (a *Client) Call(result any, args ...string) (bool, error) {
	args = append(args, "--output", "json", "--no-cli-pager", "--cli-connect-timeout", "10", "--cli-read-timeout", "30")
	cmd := exec.Command("aws", args...)
	cmd.Env = a.Env
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if a.RenewalFailure != nil {
			if renewal := a.RenewalFailure(a.Env, stderr.String()); renewal != nil {
				return false, renewal
			}
		}
		code := regexp.MustCompile(`\(([A-Za-z0-9.]+)\) when calling`).FindStringSubmatch(stderr.String())
		if len(code) == 2 {
			if code[1] == "NoSuchEntity" {
				return false, nil
			}
			return false, &Error{Operation: strings.Join(args[:2], " "), Code: code[1]}
		}
		return false, fmt.Errorf("AWS %s failed; check AWS CLI installation, connectivity and session", strings.Join(args[:2], " "))
	}
	if result != nil && json.Unmarshal(out.Bytes(), result) != nil {
		return false, errors.New("invalid AWS response")
	}
	return true, nil
}
