package workflow

import (
	"encoding/json"
	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
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

func TestMergeLocalAssociations(t *testing.T) {
	a := strings.Repeat("AA:", 31) + "AA"
	b := strings.Repeat("BB:", 31) + "BB"
	existing := []assetlinks.Application{{PackageName: "com.example.app", Fingerprints: []string{a}}}
	incoming := []assetlinks.Application{{PackageName: "com.example.app", Fingerprints: []string{b}}}
	merged, changed, err := mergeAssociations(existing, incoming)
	if err != nil || !changed || len(merged[0].Fingerprints) != 2 || len(existing[0].Fingerprints) != 1 {
		t.Fatalf("merge failed: %v %v", merged, err)
	}
	_, changed, err = mergeAssociations(merged, incoming)
	if err != nil || changed {
		t.Fatal("repeat import must not change approvals")
	}
	_, _, err = mergeAssociations(existing, []assetlinks.Application{{PackageName: "invalid", Fingerprints: []string{b}}})
	if err == nil {
		t.Fatal("invalid package accepted")
	}
}

func TestApprovedAssociationUpdateReachesExistingStack(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	f := &bootstrapFake{list: `[{"name":"dev"}]`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, config: `{"attestra-auth-email:androidAssociations":{"value":"[]"},"attestra-auth-email:proofKey":{"value":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","secret":true}}`}
	approved := `[{"packageName":"com.example.app","sha256CertFingerprints":["` + strings.Repeat("AA:", 31) + `AA"]}]`
	if err := bootstrapStack(f, o, map[string]string{"attestra-auth-email:androidAssociations": approved}, false, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "attestra-auth-email:androidAssociations -- "+approved) {
		t.Fatal("approved update was skipped", f.calls)
	}
}
