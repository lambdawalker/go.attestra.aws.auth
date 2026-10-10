package aws

import "strings"

const SourceEnvironmentKey = "ATTESTRA_AWS_CREDENTIAL_SOURCE"

func WithoutCredentials(base []string) []string {
	result := []string{}
	for _, entry := range base {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if key == SourceEnvironmentKey || strings.HasPrefix(key, "AWS_") || strings.HasPrefix(key, "PULUMI_") {
			continue
		}
		// Do not let the developer's cross-compilation flags affect host build tools.
		if key == "GOOS" || key == "GOARCH" || key == "CGO_ENABLED" {
			continue
		}
		result = append(result, entry)
	}
	return result
}
