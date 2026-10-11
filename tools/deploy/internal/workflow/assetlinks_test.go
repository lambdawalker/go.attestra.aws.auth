package workflow

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type associationTransport struct {
	status        int
	content, body string
}

func (t associationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{"Content-Type": []string{t.content}}, Body: io.NopCloser(strings.NewReader(t.body)), Request: r}, nil
}
func TestAssetLinksReadiness(t *testing.T) {
	for _, tc := range []struct {
		s    int
		c, b string
		ok   bool
	}{{200, "application/json", "[]", true}, {200, "application/json; charset=utf-8", "[]", true}, {302, "application/json", "[]", false}, {200, "text/html", "[]", false}, {200, "application/json", "[{}]", false}, {403, "application/json", "[]", false}} {
		client := &http.Client{Transport: associationTransport{tc.s, tc.c, tc.b}}
		err := checkAssetLinks(client, "https://dev.example.com/.well-known/assetlinks.json", "[]")
		if (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
}
func TestAssociationTargetsAndTeardown(t *testing.T) {
	targets := associationTargets("qa", true)
	if len(targets) != 3 || !strings.Contains(targets[0], "providers:aws::app-edge") {
		t.Fatal(targets)
	}
	for kind, want := range map[string]string{"aws:cloudfront/distribution:Distribution": "distribution", "aws:cloudfront/originAccessControl:OriginAccessControl": "origin-access-control", "aws:s3/bucketObjectv2:BucketObjectv2": "bucket"} {
		p := teardownResourceProbe(kind, "urn", "id", map[string]json.RawMessage{"bucket": json.RawMessage(`"app-associations-test"`)})
		if p.Kind != want {
			t.Fatal(p)
		}
	}
}
