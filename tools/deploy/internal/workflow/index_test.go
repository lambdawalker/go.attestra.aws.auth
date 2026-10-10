package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	envregistry "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/registry"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/registry"
)

func TestIndexEndpointRejectsCredentialExfiltration(t *testing.T) {
	env := []string{"AWS_ACCESS_KEY_ID=example", "AWS_SECRET_ACCESS_KEY=example"}
	for _, address := range []string{"https://evil.example", "http://abc.execute-api.us-east-2.amazonaws.com", "https://abc.execute-api.us-east-2.amazonaws.com.evil.example", "https://abc.execute-api.us-east-2.amazonaws.com/?x=1", "https://user@abc.execute-api.us-east-2.amazonaws.com"} {
		if _, err := newIndexClient(env, address, "us-east-2", "dev"); err == nil {
			t.Fatal("accepted", address)
		}
	}
	if _, err := newIndexClient(env, "https://abc.execute-api.us-east-2.amazonaws.com", "us-east-2", "dev"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexPendingReceiptRoundTrip(t *testing.T) {
	path := indexPendingPath(stateTestDir(t), "qa")
	p := pendingIndex{API: "https://example", Environment: "qa", Receipt: registry.Receipt{Token: "receipt", Revision: 12}, Deployed: true}
	if e := savePendingIndex(path, p); e != nil {
		t.Fatal(e)
	}
	p.Ready = true
	if e := savePendingIndex(path, p); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var out pendingIndex
	if e = json.Unmarshal(b, &out); e != nil || out != p {
		t.Fatalf("%+v %v", out, e)
	}
}
func TestRegistrySourceHashIgnoresGeneratedConfig(t *testing.T) {
	root := stateTestDir(t)
	for _, dir := range []string{"registry", "cmd/index", "infra-index"} {
		os.MkdirAll(filepath.Join(root, dir), 0755)
	}
	for _, name := range []string{"go.mod", "go.sum", "registry/service.go", "infra-index/Pulumi.yaml"} {
		os.WriteFile(filepath.Join(root, name), []byte("one"), 0600)
	}
	first, e := indexSourceHash(root)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "infra-index/Pulumi.shared.yaml"), []byte("generated"), 0600)
	next, _ := indexSourceHash(root)
	if next != first {
		t.Fatal("generated YAML invalidated source hash")
	}
	os.WriteFile(filepath.Join(root, "registry/service.go"), []byte("two"), 0600)
	next, _ = indexSourceHash(root)
	if first == next {
		t.Fatal("code update not detected")
	}
}
func TestIndexPolicyScopesWritesToOneEnvironment(t *testing.T) {
	b, _ := json.Marshal(indexPublicationPolicy("arn:aws:execute-api:us-east-2:123456789012:abc", "qa"))
	if !strings.Contains(string(b), `abc/*/POST/v1/environments/qa/changes`) {
		t.Fatal(string(b))
	}
	if strings.Contains(string(b), "dynamodb") {
		t.Fatal("deployment role has direct registry data access")
	}
}
func TestPendingPublicationRetriesWithoutDeployment(t *testing.T) {
	fail := true
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var change registry.Change
		json.NewDecoder(r.Body).Decode(&change)
		if change.Operation != "publish" || change.Revision != 7 {
			t.Errorf("unexpected operation %+v", change)
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	c := &envregistry.Client{URL: server.URL, Region: "us-east-2", Environment: "dev", HTTP: server.Client(), Credentials: aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}}
	o := options{Root: stateTestDir(t), Stack: "dev", PublishOnly: true}
	path := indexPendingPath(o.Root, o.Stack)
	p := pendingIndex{API: c.URL, Region: c.Region, Environment: "dev", Deployed: true, Ready: true, Receipt: registry.Receipt{Token: strings.Repeat("a", 48), Revision: 7}}
	if e := savePendingIndex(path, p); e != nil {
		t.Fatal(e)
	}
	r := &processRunner{}
	if e := r.withIndexClient(o, c, nil); e == nil {
		t.Fatal("expected failure")
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("lost recovery receipt")
	}
	fail = false
	if e := r.withIndexClient(o, c, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("completed receipt not removed")
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}
func TestDeploymentFailureReleasesLease(t *testing.T) {
	operations := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c registry.Change
		json.NewDecoder(r.Body).Decode(&c)
		operations = append(operations, c.Operation)
		if c.Operation == "begin" {
			json.NewEncoder(w).Encode(registry.Receipt{Token: c.Token, Revision: 1})
		} else {
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	c := &envregistry.Client{URL: server.URL, Region: "us-east-2", Environment: "dev", HTTP: server.Client(), Credentials: aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}}
	o := options{Root: stateTestDir(t), Stack: "dev"}
	err := (&processRunner{}).withIndexClient(o, c, func() error { return fmt.Errorf("deployment failed") })
	if err == nil || strings.Join(operations, ",") != "begin,abandon" {
		t.Fatalf("%v %v", err, operations)
	}
	if _, e := os.Stat(indexPendingPath(o.Root, o.Stack)); !os.IsNotExist(e) {
		t.Fatal("abandoned receipt retained")
	}
}
func TestSetupLeaseCoversConfigurationAndPassesToDeployment(t *testing.T) {
	operations := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c registry.Change
		json.NewDecoder(r.Body).Decode(&c)
		operations = append(operations, c.Operation)
		if c.Operation == "begin" {
			json.NewEncoder(w).Encode(registry.Receipt{Token: c.Token, Revision: 5})
		} else {
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	client := &envregistry.Client{URL: server.URL, Region: "us-east-2", Environment: "dev", HTTP: server.Client(), Credentials: aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}}
	o := options{Root: stateTestDir(t), Stack: "dev"}
	runner := &processRunner{}
	if e := runner.acquireSetupIndex(o, client); e != nil {
		t.Fatal(e)
	}
	if runner.indexLease == nil {
		t.Fatal("setup has no lock")
	}
	if e := runner.acquireSetupIndex(o, client); e != nil {
		t.Fatal(e)
	}
	// A separate process cannot treat this as its own active setup lease.
	if e := (&processRunner{}).withIndexClient(o, client, func() error { t.Fatal("other process deployed"); return nil }); e == nil {
		t.Fatal("interrupted/foreign receipt accepted")
	}
	e := runner.withIndexClient(o, client, func() error { return fmt.Errorf("deployment failed") })
	if e == nil || runner.indexLease != nil || strings.Join(operations, ",") != "begin,abandon" {
		t.Fatalf("%v %v", e, operations)
	}
}

func TestIndexReadinessReportsLastHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":"registry unavailable"}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	err := waitIndexHTTPContext(ctx, server.URL, server.Client(), time.Millisecond, &output)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("missing failure details: %v", err)
	}
	if !strings.Contains(output.String(), "HTTP 503") {
		t.Fatal(output.String())
	}
}
func TestIndexReadinessRecoversAndValidatesSchema(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"schemaVersion":2}`)
			return
		}
		fmt.Fprint(w, `{"schemaVersion":1,"environments":[]}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := waitIndexHTTPContext(ctx, server.URL, server.Client(), time.Millisecond, &output); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(output.String(), "schemaVersion 2") {
		t.Fatalf("calls=%d output=%s", calls, &output)
	}
}
