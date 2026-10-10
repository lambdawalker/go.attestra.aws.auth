package workflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lambdawalker/go.attestra.aws.auth/registry"
)

type healthRunner struct{ fail, index string }

func (r healthRunner) Exec(_ string, _ bool, name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	if r.fail != "" && strings.Contains(cmd, r.fail) {
		return nil, errors.New("private diagnostic")
	}
	switch {
	case strings.Contains(cmd, "stack output"):
		return json.Marshal(map[string]any{"apiUrl": "https://qa.api.example.com", "userPoolId": "us-east-2_pool", "clientId": "client123", "captureEnabled": false, "indexUrl": r.index})
	case strings.Contains(cmd, "config --json"):
		return []byte(`{"attestra-auth-email:senderDomain":{"value":"qa.info.example.com"},"attestra-auth-email:appOrigin":{"value":"https://qa.example.com"},"attestra-auth-email:apiDomain":{"value":"qa.api.example.com"}}`), nil
	case strings.Contains(cmd, "get-identity-verification-attributes"):
		return []byte(`{"VerificationAttributes":{"qa.info.example.com":{"VerificationStatus":"Success"}}}`), nil
	case strings.Contains(cmd, "get-identity-dkim-attributes"):
		return []byte(`{"DkimAttributes":{"qa.info.example.com":{"DkimVerificationStatus":"Success"}}}`), nil
	case strings.Contains(cmd, "describe-user-pool-client"):
		return []byte(`{"UserPoolClient":{"UserPoolId":"us-east-2_pool","ClientId":"client123"}}`), nil
	}
	return nil, errors.New("unexpected command: " + cmd)
}
func healthServer(t *testing.T, broken string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Origin", "https://qa.example.com")
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET")
			w.Header().Set("Access-Control-Allow-Headers", "content-type,authorization")
			if broken == "headers" {
				w.Header().Del("Access-Control-Allow-Headers")
			}
			if broken == "cors" {
				w.Header().Set("Access-Control-Allow-Origin", "https://wrong.example.com")
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/auth/status" {
			if broken == "redirect" {
				http.Redirect(w, r, "https://other.example.com", 302)
				return
			}
			if broken == "wrong401" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			if broken == "lambda" {
				w.WriteHeader(502)
				return
			}
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"sign_in_required"}`))
			return
		}
		c := registry.Configuration{APIURL: "https://qa.api.example.com", AWSRegion: "us-east-2", CognitoUserPoolID: "us-east-2_pool", CognitoClientID: "client123"}
		hash := registry.Hash(c)
		if broken == "index" {
			hash = "stale"
		}
		json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 1, "environments": []registry.Entry{{ID: "qa", Config: c, ConfigHash: hash}}})
	}))
}

// Preserve the requested public hostname while routing exclusively to a local TLS server.
type healthTransport struct {
	target string
	base   http.RoundTripper
}

func (h healthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.Host = h.target
	return h.base.RoundTrip(clone)
}
func TestEnvironmentHealthDetectsFailuresWithoutSecrets(t *testing.T) {
	for _, broken := range []string{"", "cors", "headers", "redirect", "wrong401", "lambda", "index", "describe-user-pool-client", "get-identity-dkim-attributes"} {
		t.Run(broken, func(t *testing.T) {
			server := healthServer(t, broken)
			defer server.Close()
			client := server.Client()
			client.Transport = healthTransport{strings.TrimPrefix(server.URL, "https://"), client.Transport}
			report := checkEnvironmentHealth(healthRunner{fail: broken, index: "https://index.example.com/v1/environments"}, options{Stack: "qa", Region: "us-east-2"}, client)
			if (report.Err() == nil) != (broken == "") {
				t.Fatalf("unexpected readiness: %+v", report)
			}
			b, _ := json.Marshal(report)
			if strings.Contains(string(b), "private diagnostic") {
				t.Fatal("diagnostic leaked")
			}
		})
	}
}
func TestEnvironmentHealthMissingOutputsFails(t *testing.T) {
	report := checkEnvironmentHealth(healthRunner{fail: "stack output"}, options{Stack: "qa", Region: "us-east-2"}, http.DefaultClient)
	if report.Err() == nil {
		t.Fatal("missing outputs reported healthy")
	}
}
