package envreset

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/lambdawalker/go.attestra.aws.auth/internal/awshttp"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func httpBackend(t *testing.T, f transport) AWSBackend {
	t.Helper()
	return AWSBackend{Manifest: Manifest{Bucket: "evidence-dev", Functions: []string{"api-dev", "worker-dev"}}, HTTP: awshttp.Client{Config: aws.Config{Region: "us-east-2", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	})}, HTTP: &http.Client{Transport: f}}}
}
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func TestDeleteEvidenceVersionsAndMarkersOnly(t *testing.T) {
	count := 0
	b := httpBackend(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "evidence-dev.s3.us-east-2.amazonaws.com" || r.Header.Get("Authorization") == "" {
			t.Fatal("unsigned or wrong scope", r.URL)
		}
		if r.Method == "GET" {
			return response(200, `<ListVersionsResult><Version><Key>a/b +.jpg</Key><VersionId>v+1</VersionId></Version><DeleteMarker><Key>a/b +.jpg</Key><VersionId>marker</VersionId></DeleteMarker></ListVersionsResult>`), nil
		}
		if r.Method != "DELETE" || r.URL.Path != "/a/b +.jpg" || (r.URL.Query().Get("versionId") != "v+1" && r.URL.Query().Get("versionId") != "marker") {
			t.Fatal("unsafe delete", r.Method, r.URL)
		}
		count++
		return response(204, ""), nil
	})
	done, err := b.Clear(context.Background(), "objects")
	if err != nil || done || count != 2 {
		t.Fatal(done, count, err)
	}
}
func TestRestoreExactConcurrencyAndRejectWrongInventory(t *testing.T) {
	calls := 0
	b := httpBackend(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Path, "api-dev") {
			if r.Method != "DELETE" {
				t.Fatal("invented concurrency")
			}
		} else {
			body, _ := io.ReadAll(r.Body)
			if r.Method != "PUT" || string(body) != `{"ReservedConcurrentExecutions":2}` {
				t.Fatal("lost original concurrency", string(body))
			}
		}
		return response(200, "{}"), nil
	})
	if err := b.Restore(context.Background(), map[string]int{"api-dev": -1, "worker-dev": 2}); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	if err := b.Pause(context.Background(), map[string]int{"api-prod": -1, "worker-dev": 2}); err != ErrInvalid || calls != 2 {
		t.Fatal("cross-environment inventory accepted")
	}
}
func TestControlPersistsPrivateRecoveryFields(t *testing.T) {
	original := State{Revision: 3, Phase: "drain", ValidAfter: 12, LeaseUntil: 99, Concurrency: map[string]int{"api": -1}}
	item, err := attributevalue.MarshalMap(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err = attributevalue.UnmarshalMap(item, &restored); err != nil || restored.ValidAfter != 12 || restored.LeaseUntil != 99 || restored.Concurrency["api"] != -1 {
		t.Fatal(restored, err)
	}
}
