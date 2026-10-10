package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	model "github.com/lambdawalker/go.attestra.aws.auth/registry"
)

func TestIndexClientSignsAndRetriesExactReceipt(t *testing.T) {
	count := 0
	var original string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if !strings.Contains(r.Header.Get("Authorization"), "/execute-api/aws4_request") {
			t.Error("request unsigned")
		}
		var change model.Change
		if e := json.NewDecoder(r.Body).Decode(&change); e != nil {
			t.Error(e)
		}
		if count == 1 {
			original = change.Token
			w.WriteHeader(503)
			return
		}
		if change.Token != original {
			t.Error("retry changed token")
		}
		json.NewEncoder(w).Encode(model.Receipt{Token: change.Token, Revision: 4})
	}))
	defer server.Close()
	c := Client{URL: server.URL, Region: "us-east-2", Environment: "dev", Credentials: aws.Credentials{AccessKeyID: "example", SecretAccessKey: "example"}, HTTP: server.Client()}
	r, e := c.Change(model.Change{Operation: "begin", Token: strings.Repeat("a", 48)})
	if e != nil || r.Revision != 4 || count != 2 {
		t.Fatalf("%+v %v count %d", r, e, count)
	}
}
func TestIndexUsesRenewedSigningCredentials(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if !strings.Contains(r.Header.Get("Authorization"), fmt.Sprintf("Credential=key%d/", count)) {
			t.Error("stale signing credentials")
		}
		json.NewEncoder(w).Encode(model.Receipt{Revision: 1, Token: strings.Repeat("a", 48)})
	}))
	defer server.Close()
	calls := 0
	provider := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		calls++
		return aws.Credentials{AccessKeyID: fmt.Sprintf("key%d", calls), SecretAccessKey: "test"}, nil
	})
	client := Client{URL: server.URL, Region: "us-east-2", Environment: "dev", Provider: provider, HTTP: server.Client()}
	for range 2 {
		if _, err := client.Change(model.Change{Operation: "begin", Token: strings.Repeat("a", 48)}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || count != 2 {
		t.Fatalf("provider calls %d requests %d", calls, count)
	}
}
