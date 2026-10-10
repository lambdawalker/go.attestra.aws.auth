package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
)

func TestPublishAPIDNSCreatesOnceAndRefusesConflict(t *testing.T) {
	zone := strings.Repeat("a", 32)
	records := []dns.Record{}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		if r.URL.Path == "/zones" {
			result = []dns.Zone{{ID: zone, Name: "example.com", Status: "active"}}
		} else if r.Method == "GET" {
			result = records
		} else {
			writes++
			var record dns.Record
			if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
				t.Error(err)
			}
			if record.Proxied {
				t.Error("API record must be DNS only")
			}
			record.ID = strings.Repeat("b", 32)
			records = append(records, record)
			result = record
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	defer server.Close()
	cf := dns.NewClient("test")
	cf.BaseURL = server.URL
	wizard := bootstrapWizard{cf: cf}
	desired := []dns.Record{{Type: "CNAME", Name: "dev.api.example.com", Content: "d-api.execute-api.us-east-2.amazonaws.com"}}
	for n := 0; n < 2; n++ {
		if err := wizard.publishDNS("dev.api.example.com", desired); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatal(writes)
	}
	records[0].Content = "another-service.example.com"
	if wizard.publishDNS("dev.api.example.com", desired) == nil || writes != 1 {
		t.Fatal("conflicting DNS overwritten")
	}
}
