package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
)

func TestTeardownStopsBeforeExternalCleanupOnDestroyFailure(t *testing.T) {
	calls := []string{}
	steps := []teardownStep{{"destroy", func() error { calls = append(calls, "destroy"); return errors.New("bucket not empty") }}, {"DNS", func() error { calls = append(calls, "DNS"); return nil }}, {"GitHub", func() error { calls = append(calls, "GitHub"); return nil }}}
	if runTeardownSteps(steps) == nil || len(calls) != 1 {
		t.Fatal(calls)
	}
}
func TestTeardownDNSOnlySelectsExactRecords(t *testing.T) {
	want := dns.Record{Type: "TXT", Name: "_amazonses.qa.info.example.com", Content: "our-token"}
	old := []dns.Record{{ID: strings.Repeat("a", 32), Type: "TXT", Name: want.Name, Content: `"our-token"`}, {ID: strings.Repeat("b", 32), Type: "TXT", Name: want.Name, Content: "another-environment"}}
	got, err := dns.DeletionCandidates(want, old)
	if err != nil || len(got) != 1 || got[0].ID != old[0].ID {
		t.Fatal(got, err)
	}
	want.Type = "CNAME"
	want.Content = "abc.dkim.amazonses.com"
	if _, err = dns.DeletionCandidates(want, []dns.Record{{Type: "CNAME", Name: want.Name, Content: "other.example.com"}}); err == nil {
		t.Fatal("accepted conflicting DNS target")
	}
}
func TestTeardownStateRejectsRetainedResources(t *testing.T) {
	for _, tc := range []struct {
		state string
		empty bool
	}{
		{`{"deployment":{"resources":[]}}`, true},
		{`{"deployment":{"resources":[{"type":"pulumi:pulumi:Stack"}]}}`, true},
		{`{"deployment":{"resources":[{"type":"aws:s3/bucketV2:BucketV2"}]}}`, false},
		{`bad`, false},
	} {
		if (ensureDestroyed([]byte(tc.state)) == nil) != tc.empty {
			t.Fatal(tc)
		}
	}
}

type missingEvidence struct{ code string }

func (f missingEvidence) Call(any, ...string) (bool, error) {
	return false, &awsenv.Error{Operation: "list-object-versions", Code: f.code}
}
func TestEvidencePurgeResumeAndStateProtection(t *testing.T) {
	if err := purgeEvidence(missingEvidence{"NoSuchBucket"}, "evidence-bucket", "123456789012", "s3://state-bucket"); err != nil {
		t.Fatal(err)
	}
	if err := purgeEvidence(missingEvidence{"AccessDenied"}, "evidence-bucket", "123456789012", "s3://state-bucket"); err == nil {
		t.Fatal("ignored access denial")
	}
	if err := purgeEvidence(missingEvidence{"NoSuchBucket"}, "state-bucket", "123456789012", "s3://state-bucket"); err == nil {
		t.Fatal("allowed state purge")
	}
}

func TestTeardownQuotedTXTDeletionIsResumable(t *testing.T) {
	zone := strings.Repeat("a", 32)
	approved := dns.Record{ID: strings.Repeat("b", 32), Type: "TXT", Name: "_amazonses.example.com", Content: `"our-token"`}
	other := dns.Record{ID: strings.Repeat("c", 32), Type: "TXT", Name: approved.Name, Content: "another-service"}
	present := true
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			if !strings.HasSuffix(r.URL.Path, "/"+approved.ID) {
				t.Error("wrong record deleted")
			}
			present = false
			deletes++
			fmt.Fprint(w, `{"success":true,"result":{}}`)
			return
		}
		records := []dns.Record{other}
		if present {
			records = append(records, approved)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": records})
	}))
	defer server.Close()
	c := dns.NewClient("test")
	c.BaseURL = server.URL
	for i := 0; i < 2; i++ {
		if err := c.DeleteDNS(zone, []dns.Record{approved}); err != nil {
			t.Fatal(err)
		}
	}
	if deletes != 1 {
		t.Fatal(deletes)
	}
}
func TestTeardownCapturesEvidenceBeforeDestroy(t *testing.T) {
	state := []byte(`{"deployment":{"resources":[
 {"type":"aws:ses/domainIdentity:DomainIdentity","urn":"urn:pulumi:qa::project::aws:ses/domainIdentity:DomainIdentity::email-sender","outputs":{"domain":"qa.info.example.com","verificationToken":"verify"}},
 {"type":"aws:ses/domainDkim:DomainDkim","urn":"urn:pulumi:qa::project::aws:ses/domainDkim:DomainDkim::email-sender-dkim","outputs":{"dkimTokens":["a","b","c"]}},
 {"type":"aws:s3/bucketV2:BucketV2","urn":"urn:pulumi:qa::project::aws:s3/bucketV2:BucketV2::id-evidence","id":"id-evidence-test"}
 ]}}`)
	p := teardownProgress{Repository: "owner/repo", Environment: "qa"}
	if err := captureTeardownEvidence(state, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Desired) != 4 || p.Bucket != "id-evidence-test" {
		t.Fatal(p)
	}
	path := filepath.Join(t.TempDir(), "teardown.qa.local.json")
	if err := p.save(path); err != nil {
		t.Fatal(err)
	}
	p.Destroyed = true
	if err := p.save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var resumed teardownProgress
	if json.Unmarshal(b, &resumed) != nil || !resumed.Destroyed || len(resumed.Desired) != 4 {
		t.Fatal(string(b))
	}
}

func TestTeardownSkipCloudflarePersistsWithoutDeleting(t *testing.T) {
	p := teardownProgress{DNSSkipped: true, DNS: []dns.Record{{ID: "approved-id", Name: "_amazonses.dev.example.com"}}}
	path := filepath.Join(t.TempDir(), "progress.json")
	if err := p.save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var resumed teardownProgress
	if err := json.Unmarshal(data, &resumed); err != nil {
		t.Fatal(err)
	}
	// A nil client ensures a resumed skip cannot make any Cloudflare calls.
	if err := cleanupTeardownDNS(&resumed, nil, func() error { t.Fatal("skip must not mark DNS deleted"); return nil }); err != nil {
		t.Fatal(err)
	}
	if !resumed.DNSSkipped || resumed.DNSDone || len(resumed.DNS) != 1 {
		t.Fatalf("lost skipped DNS evidence: %+v", resumed)
	}
}

func TestTeardownCapturesAPIDNS(t *testing.T) {
	data := []byte(`{"deployment":{"resources":[
 {"type":"aws:acm/certificate:Certificate","urn":"urn:pulumi:dev::project::aws:acm/certificate:Certificate::api-certificate","outputs":{"domainValidationOptions":[{"resourceRecordName":"_token.dev.api.example.com.","resourceRecordType":"CNAME","resourceRecordValue":"_validation.acm-validations.aws."}]}},
 {"type":"aws:apigatewayv2/domainName:DomainName","urn":"urn:pulumi:dev::project::aws:apigatewayv2/domainName:DomainName::api-domain","outputs":{"domainName":"dev.api.example.com","domainNameConfiguration":{"targetDomainName":"d-api.execute-api.us-east-2.amazonaws.com"}}}
 ]}}`)
	var p teardownProgress
	if err := captureTeardownEvidence(data, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Desired) != 2 || p.Desired[0].Name != "_token.dev.api.example.com" || p.Desired[1].Name != "dev.api.example.com" {
		t.Fatal(p.Desired)
	}
}
