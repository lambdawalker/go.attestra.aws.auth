package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/lambdawalker/go.attestra.aws.auth/registry"
)

type healthResult struct{ Name, Status, Detail string }
type healthReport []healthResult

func (r healthReport) Err() error {
	for _, item := range r {
		if item.Status == "FAIL" {
			return errors.New("environment health check failed; existing resources are retained. Resolve the failed checks, then rerun with -health-check")
		}
	}
	return nil
}
func healthURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
func healthIndexURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/v1/environments" {
		return false
	}
	u.Path = ""
	return healthURL(u.String())
}
func checkEnvironmentHealth(r commandRunner, o options, client *http.Client) healthReport {
	report := healthReport{}
	add := func(name string, ok bool, good, bad string) {
		status, detail := "PASS", good
		if !ok {
			status, detail = "FAIL", bad
		}
		report = append(report, healthResult{name, status, detail})
	}
	read := func(v any, name string, args ...string) bool {
		data, e := r.Exec(filepath.Join(o.Root, "infra"), true, name, args...)
		return e == nil && json.Unmarshal(data, v) == nil
	}
	var output struct {
		APIURL  string `json:"apiUrl"`
		Pool    string `json:"userPoolId"`
		Client  string `json:"clientId"`
		Index   string `json:"indexUrl"`
		Capture bool   `json:"captureEnabled"`
	}
	if !read(&output, "pulumi", "stack", "output", "--json", "--stack", o.Stack) {
		add("Stack outputs", false, "", "Cannot read deployed outputs; check AWS session, backend, passphrase, and stack.")
		return report
	}
	expected := registry.Configuration{APIURL: output.APIURL, AWSRegion: o.Region, CognitoUserPoolID: output.Pool, CognitoClientID: output.Client}
	expected.Features.IDCapture = output.Capture
	outputsOK := registry.Validate(expected) == nil && healthURL(output.APIURL)
	add("Stack outputs", outputsOK, "API and Android identifiers are present.", "Required API/Cognito outputs are missing or invalid; finish deployment.")
	var config map[string]struct{ Value string }
	if !read(&config, "pulumi", "config", "--json", "--stack", o.Stack) {
		add("Stack configuration", false, "", "Cannot read configuration; check backend and stack access.")
		return report
	}
	domain, origin := config["attestra-auth-email:senderDomain"].Value, config["attestra-auth-email:appOrigin"].Value
	if custom := config["attestra-auth-email:apiDomain"].Value; custom != "" {
		add("API hostname", output.APIURL == "https://"+custom, "Deployed URL matches the configured hostname.", "Deployed URL differs from apiDomain; complete the custom-domain deployment.")
	}
	awsRead := func(v any, args ...string) bool {
		return read(v, "aws", append(args, "--region", o.Region, "--output", "json", "--no-cli-pager", "--cli-connect-timeout", "10", "--cli-read-timeout", "30")...)
	}
	var verification struct {
		VerificationAttributes map[string]struct{ VerificationStatus string }
	}
	ok := domain != "" && awsRead(&verification, "ses", "get-identity-verification-attributes", "--identities", domain) && verification.VerificationAttributes[domain].VerificationStatus == "Success"
	add("SES identity", ok, "Sender domain is verified.", "Sender verification is missing, pending, or unreadable; check SES DNS and AWS permissions.")
	var dkim struct {
		DkimAttributes map[string]struct{ DkimVerificationStatus string }
	}
	ok = domain != "" && awsRead(&dkim, "ses", "get-identity-dkim-attributes", "--identities", domain) && dkim.DkimAttributes[domain].DkimVerificationStatus == "Success"
	add("SES DKIM", ok, "DKIM is verified.", "Check the sender's DKIM CNAMEs and allow DNS propagation; verify AWS read access.")
	if outputsOK {
		var cognito struct {
			UserPoolClient struct{ UserPoolId, ClientId string }
		}
		ok = awsRead(&cognito, "cognito-idp", "describe-user-pool-client", "--user-pool-id", output.Pool, "--client-id", output.Client) && cognito.UserPoolClient.UserPoolId == output.Pool && cognito.UserPoolClient.ClientId == output.Client
		add("Cognito client", ok, "App client exists in the deployed user pool.", "Check Cognito pool/client IDs, region, and read permissions.")
		// Requests carry no AWS credentials, user tokens, or personal data. Redirects
		// are rejected so a different service cannot make this check appear healthy.
		probeClient := *client
		probeClient.Timeout = 15 * time.Second
		probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		probe := func(method, address string, headers map[string]string) (int, http.Header, []byte) {
			req, e := http.NewRequest(method, address, nil)
			if e != nil {
				return 0, nil, nil
			}
			for key, value := range headers {
				req.Header.Set(key, value)
			}
			res, e := probeClient.Do(req)
			if e != nil {
				return 0, nil, nil
			}
			defer res.Body.Close()
			data, e := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
			if e != nil || len(data) > 1024*1024 {
				return 0, nil, nil
			}
			return res.StatusCode, res.Header, data
		}
		endpoint := strings.TrimRight(output.APIURL, "/") + "/auth/status"
		code, _, body := probe("POST", endpoint, nil)
		var rejection struct{ Error string }
		ok = code == 401 && json.Unmarshal(body, &rejection) == nil && rejection.Error == "sign_in_required"
		add("API HTTPS and Lambda", ok, "DNS, TLS, API routing, and the auth-status Lambda responded correctly.", "Expected the auth-status sign-in-required response; check DNS, TLS, API mapping, and Lambda logs, then retry.")
		code, headers, _ := probe("OPTIONS", endpoint, map[string]string{"Origin": origin, "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type,authorization"})
		methods := "," + strings.ReplaceAll(strings.ToUpper(headers.Get("Access-Control-Allow-Methods")), " ", "") + ","
		allowedHeaders := "," + strings.ReplaceAll(strings.ToLower(headers.Get("Access-Control-Allow-Headers")), " ", "") + ","
		ok = strings.Contains(allowedHeaders, ",content-type,") && strings.Contains(allowedHeaders, ",authorization,") && healthURL(origin) && code >= 200 && code < 300 && headers.Get("Access-Control-Allow-Origin") == origin && strings.Contains(methods, ",POST,")
		add("API CORS", ok, "API allows the configured app origin.", "Check appOrigin and API Gateway CORS configuration; retry after DNS propagation.")
		if output.Index == "" {
			report = append(report, healthResult{"Environment index", "WARN", "No index configured; rerun setup to register this environment."})
		} else if !healthIndexURL(output.Index) {
			add("Environment index", false, "", "Index URL is invalid; rerun setup.")
		} else {
			code, _, body = probe("GET", output.Index, nil)
			var listing struct {
				SchemaVersion int              `json:"schemaVersion"`
				Environments  []registry.Entry `json:"environments"`
			}
			match := false
			if code == 200 && json.Unmarshal(body, &listing) == nil && listing.SchemaVersion == 1 {
				for _, entry := range listing.Environments {
					if entry.ID == o.Stack && entry.Config == expected && entry.ConfigHash == registry.Hash(expected) {
						match = true
					}
				}
			}
			add("Environment index", match, "Published environment matches deployed configuration and hash.", "Index is unreachable, missing this environment, or stale; check index DNS/publication and retry.")
		}
	}
	if !output.Capture {
		report = append(report, healthResult{"ID capture", "WARN", "Disabled by configuration; this is not a deployment failure."})
	}
	report = append(report, healthResult{"Coverage", "INFO", "No users, emails, or ID uploads were created. SES sandbox restrictions and full sign-in/upload flows still require separate testing."})
	return report
}
func runEnvironmentHealth(r commandRunner, o options) error {
	fmt.Printf("\nFinal environment health check • %s • %s\n", o.Stack, o.Region)
	report := checkEnvironmentHealth(r, o, &http.Client{})
	for _, item := range report {
		fmt.Printf("[%s] %s: %s\n", item.Status, item.Name, item.Detail)
	}
	if err := report.Err(); err != nil {
		return err
	}
	fmt.Println("✓ Environment health checks passed.")
	return nil
}
