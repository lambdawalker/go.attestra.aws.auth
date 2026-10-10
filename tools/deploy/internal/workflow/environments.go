package workflow

import (
	"errors"
	"regexp"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func applicationDefaults(environment, base string) (map[string]string, error) {
	if err := ui.ValidateEnvironment(environment); err != nil {
		return nil, err
	}
	label := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	parts := strings.Split(base, ".")
	if len(parts) < 2 || len(environment+".info."+base) > 253 {
		return nil, errors.New("enter a base domain such as example.com, without a scheme or path")
	}
	for _, p := range parts {
		if len(p) > 63 || !label.MatchString(p) {
			return nil, errors.New("invalid base domain; use lowercase DNS labels")
		}
	}
	if !regexp.MustCompile(`^[a-z]{2,}$`).MatchString(parts[len(parts)-1]) {
		return nil, errors.New("base domain must have a DNS top-level domain")
	}
	sender := environment + ".info." + base
	api := environment + ".api." + base
	if environment == "prod" {
		api = "api." + base
	}
	return map[string]string{
		"attestra-auth-email:apiDomain":     api,
		"attestra-auth-email:appOrigin":     "https://" + environment + "." + base,
		"attestra-auth-email:senderDomain":  sender,
		"attestra-auth-email:senderAddress": "verify@" + sender,
	}, nil
}
