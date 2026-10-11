// Package assetlinks validates public Android signing associations. It never accepts private keys.
package assetlinks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Application struct {
	PackageName  string   `json:"packageName"`
	Fingerprints []string `json:"sha256CertFingerprints"`
}
type Statement struct {
	Relation []string `json:"relation"`
	Target   Target   `json:"target"`
}
type Target struct {
	Namespace    string   `json:"namespace"`
	PackageName  string   `json:"package_name"`
	Fingerprints []string `json:"sha256_cert_fingerprints"`
}

func Render(apps []Application) (string, error) {
	if len(apps) > 100 {
		return "", fmt.Errorf("too many Android applications")
	}
	statements := make([]Statement, 0, len(apps))
	seen := map[string]bool{}
	for _, app := range apps {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`).MatchString(app.PackageName) || seen[app.PackageName] {
			return "", fmt.Errorf("invalid or duplicate Android package %q", app.PackageName)
		}
		seen[app.PackageName] = true
		if len(app.Fingerprints) == 0 || len(app.Fingerprints) > 100 {
			return "", fmt.Errorf("package %s requires 1–100 approved fingerprints", app.PackageName)
		}
		fingerprints := []string{}
		unique := map[string]bool{}
		for _, fp := range app.Fingerprints {
			fp = strings.ToUpper(fp)
			if !regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`).MatchString(fp) {
				return "", fmt.Errorf("invalid SHA-256 certificate fingerprint for %s", app.PackageName)
			}
			if unique[fp] {
				return "", fmt.Errorf("duplicate fingerprint for %s", app.PackageName)
			}
			unique[fp] = true
			fingerprints = append(fingerprints, fp)
		}
		sort.Strings(fingerprints)
		statements = append(statements, Statement{[]string{"delegate_permission/common.handle_all_urls", "delegate_permission/common.get_login_creds"}, Target{"android_app", app.PackageName, fingerprints}})
	}
	sort.Slice(statements, func(i, j int) bool { return statements[i].Target.PackageName < statements[j].Target.PackageName })
	b, err := json.MarshalIndent(statements, "", "  ")
	return string(b) + "\n", err
}
