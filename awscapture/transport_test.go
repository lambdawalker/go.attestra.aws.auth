package awscapture

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type credentials struct{}

func (credentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "test-access", SecretAccessKey: "test-secret", SessionToken: "test-session"}, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPresignBindsChecksumEncryptionTypeAndExpiry(t *testing.T) {
	tr := Transport{Config: aws.Config{Region: "us-east-1", Credentials: credentials{}}, Bucket: "capture-test"}
	signed, headers, e := tr.Presign(context.Background(), capture.Upload{Key: "uploads/owner/capture/image.jpg", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=", Size: 12})
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(signed)
	if e != nil {
		t.Fatal(e)
	}
	if u.Query().Get("X-Amz-Expires") != "300" || u.Query().Get("X-Amz-Security-Token") != "test-session" {
		t.Fatal("expiry or session token missing")
	}
	for _, h := range []string{"content-length", "content-type", "x-amz-checksum-sha256", "x-amz-server-side-encryption"} {
		if !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), h) {
			t.Fatal("header not signed", h)
		}
	}
	if headers["x-amz-server-side-encryption"] != "AES256" {
		t.Fatal("missing encryption")
	}
}
func TestReadsPinnedVersionWithBoundedBody(t *testing.T) {
	tr := Transport{Config: aws.Config{Region: "us-east-1", Credentials: credentials{}}, Bucket: "capture-test", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("versionId") != "pinned" {
			t.Fatal("read latest instead of pinned")
		}
		if r.Header.Get("x-amz-content-sha256") == "" {
			t.Fatal("missing payload hash")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", int(capture.MaxBytes)+1))), Header: http.Header{}}, nil
	})}}
	_, e := tr.Read(context.Background(), capture.Asset{Key: "uploads/owner/capture/file.jpg", VersionID: "pinned"})
	if e != capture.ErrImage {
		t.Fatal("unbounded read", e)
	}
}
func TestCleanupDeletesExactListedVersions(t *testing.T) {
	var deleted []string
	listed := map[string]bool{}
	tr := Transport{Config: aws.Config{Region: "us-east-1", Credentials: credentials{}}, Bucket: "capture-test", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := "<ListVersionsResult/>"
		status := 200
		if r.Method == "DELETE" {
			version := r.URL.Query().Get("versionId")
			if version != "version-one" && version != "marker-one" {
				t.Fatalf("wrong version ID: %q", version)
			}
			deleted = append(deleted, version)
			status = 204
		} else {
			prefix := r.URL.Query().Get("prefix")
			if !listed[prefix] {
				listed[prefix] = true
				body = "<ListVersionsResult><Version><Key>" + prefix + "one.jpg</Key><VersionId>version-one</VersionId></Version><DeleteMarker><Key>" + prefix + "one.jpg</Key><VersionId>marker-one</VersionId></DeleteMarker></ListVersionsResult>"
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}}
	if e := tr.DeleteCapture(context.Background(), "uploads/owner/capture/"); e != nil {
		t.Fatal(e)
	}
	if len(deleted) != 4 {
		t.Fatal("original/derivative versions or markers missed", deleted)
	}
}
