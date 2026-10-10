package dns

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDNSPlanPreservesExistingRecords(t *testing.T) {
	txt := Record{Type: "TXT", Name: "_amazonses.qa.info.example.com", Content: "expected"}
	cname := Record{Type: "CNAME", Name: "abc._domainkey.qa.info.example.com", Content: "abc.dkim.amazonses.com"}
	for _, tc := range []struct {
		name     string
		want     Record
		existing []Record
		action   string
		bad      bool
	}{
		{"new TXT", txt, nil, "create", false},
		{"quoted TXT", txt, []Record{{Type: "TXT", Content: `"expected"`}}, "keep", false},
		{"another TXT preserved", txt, []Record{{Type: "TXT", Content: "other"}}, "create", false},
		{"correct CNAME", cname, []Record{{Type: "CNAME", Content: "ABC.dkim.amazonses.com."}}, "keep", false},
		{"disable proxy", cname, []Record{{ID: strings.Repeat("a", 32), Type: "CNAME", Content: cname.Content, Proxied: true}}, "unproxy", false},
		{"wrong target", cname, []Record{{Type: "CNAME", Content: "other.example.com"}}, "", true},
		{"other record type", cname, []Record{{Type: "TXT", Content: "other"}}, "", true},
		{"TXT conflicts CNAME", txt, []Record{{Type: "CNAME", Content: "other"}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change, err := PlanRecord(tc.want, tc.existing)
			if (err != nil) != tc.bad || (!tc.bad && change.Action != tc.action) {
				t.Fatal(change, err)
			}
		})
	}
}
func TestCloudflareCreateVerifyAndRerun(t *testing.T) {
	zone := strings.Repeat("a", 32)
	records := []Record{}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hidden-token" {
			t.Error("no auth")
		}
		if r.URL.Path != "/zones/"+zone+"/dns_records" {
			t.Error(r.URL.Path)
		}
		if r.Method == "POST" {
			var record Record
			json.NewDecoder(r.Body).Decode(&record)
			records = append(records, record)
			writes++
			fmt.Fprint(w, `{"success":true,"result":{}}`)
			return
		}
		selected := []Record{}
		for _, v := range records {
			if v.Name == r.URL.Query().Get("name") {
				selected = append(selected, v)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": selected, "result_info": map[string]int{"total_pages": 1}})
	}))
	defer server.Close()
	c := NewClient("hidden-token")
	c.BaseURL = server.URL
	desired := []Record{{Type: "TXT", Name: "_amazonses.qa.info.example.com", Content: "token"}, {Type: "CNAME", Name: "abc._domainkey.qa.info.example.com", Content: "abc.dkim.amazonses.com"}}
	for i := 0; i < 2; i++ {
		plan, err := c.Plan(zone, desired)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Apply(zone, plan); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 2 {
		t.Fatal("rerun duplicated records", writes)
	}
}
func TestCloudflareFailureDoesNotLeakTokenOrBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, "hidden-token private-body")
	}))
	defer server.Close()
	c := NewClient("hidden-token")
	c.BaseURL = server.URL
	_, err := c.Zones("qa.info.example.com")
	if err == nil || strings.Contains(err.Error(), "hidden-token") || strings.Contains(err.Error(), "private-body") {
		t.Fatal(err)
	}
}

func TestCloudflareZonePaginationAndDomainBoundary(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("page") != fmt.Sprint(calls) {
			t.Error("wrong page")
		}
		zones := []Zone{{ID: strings.Repeat("a", 32), Name: "notexample.com", Status: "active"}, {ID: strings.Repeat("b", 32), Name: "example.com", Status: "pending"}}
		if calls == 2 {
			zones = []Zone{{ID: strings.Repeat("c", 32), Name: "example.com", Status: "active"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": zones, "result_info": map[string]int{"total_pages": 2}})
	}))
	defer server.Close()
	c := NewClient("token")
	c.BaseURL = server.URL
	zones, err := c.Zones("qa.info.example.com")
	if err != nil || len(zones) != 1 || zones[0].ID != strings.Repeat("c", 32) || calls != 2 {
		t.Fatal(zones, err, calls)
	}
}
func TestCloudflareConflictOnLaterPageStopsPlan(t *testing.T) {
	calls, writes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			writes++
		}
		records := []Record{}
		if r.URL.Query().Get("page") == "2" {
			records = append(records, Record{Type: "CNAME", Name: "_amazonses.example.com", Content: "other.example.com"})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": records, "result_info": map[string]int{"total_pages": 2}})
	}))
	defer server.Close()
	c := NewClient("token")
	c.BaseURL = server.URL
	_, err := c.Plan(strings.Repeat("a", 32), []Record{{Type: "TXT", Name: "_amazonses.example.com", Content: "token"}})
	if err == nil || writes != 0 || calls != 2 {
		t.Fatal(err, writes, calls)
	}
}
func TestCloudflareRedirectDoesNotForwardToken(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	c := NewClient("token")
	c.BaseURL = server.URL
	if _, err := c.Zones("example.com"); err == nil || reached {
		t.Fatal("redirect followed", err)
	}
}
func TestCloudflarePartialWriteCanResume(t *testing.T) {
	zone := strings.Repeat("a", 32)
	desired := []Record{{Type: "TXT", Name: "_amazonses.example.com", Content: "token"}, {Type: "CNAME", Name: "abc._domainkey.example.com", Content: "abc.dkim.amazonses.com"}}
	records := []Record{}
	fail := true
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var record Record
			json.NewDecoder(r.Body).Decode(&record)
			if record.Type == "CNAME" && fail {
				w.WriteHeader(429)
				return
			}
			records = append(records, record)
			writes++
			fmt.Fprint(w, `{"success":true,"result":{}}`)
			return
		}
		selected := []Record{}
		for _, record := range records {
			if record.Name == r.URL.Query().Get("name") {
				selected = append(selected, record)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": selected})
	}))
	defer server.Close()
	c := NewClient("token")
	c.BaseURL = server.URL
	plan, err := c.Plan(zone, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(zone, plan); err == nil || writes != 1 {
		t.Fatal("expected partial failure", err, writes)
	}
	fail = false
	plan, err = c.Plan(zone, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(zone, plan); err != nil || writes != 2 {
		t.Fatal("resume duplicated records", err, writes)
	}
}

func TestCloudflareUnproxyPatchesOnlyProxySetting(t *testing.T) {
	zone, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
	record := Record{ID: id, Type: "CNAME", Name: "abc._domainkey.example.com", Content: "abc.dkim.amazonses.com", Proxied: true}
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			patches++
			if r.URL.Path != "/zones/"+zone+"/dns_records/"+id {
				t.Error(r.URL.Path)
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["proxied"] != false {
				t.Error("unexpected mutation", body)
			}
			record.Proxied = false
			fmt.Fprint(w, `{"success":true,"result":{}}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected method", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []Record{record}})
	}))
	defer server.Close()
	c := NewClient("token")
	c.BaseURL = server.URL
	desired := record
	desired.Proxied = false
	plan, err := c.Plan(zone, []Record{desired})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(zone, plan); err != nil || patches != 1 {
		t.Fatal(err, patches)
	}
}

func TestCloudflareErrorDiagnostics(t *testing.T) {
	for _, status := range []int{400, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"success":false,"errors":[{"code":6003,"message":"Invalid request headers","error_chain":[{"code":6111,"message":"Invalid format for Authorization header hidden-token\n\u001b[31m"}]}]}`)
			}))
			defer server.Close()
			client := NewClient("hidden-token")
			client.BaseURL = server.URL
			_, err := client.Request("GET", "/zones?private=query", nil, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, want := range []string{"GET /zones", "6003", "6111", "Invalid request headers", "Invalid format for Authorization header"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %q", want, err)
				}
			}
			for _, unwanted := range []string{"hidden-token", "private=query", "\n", "\x1b", "check zone-scoped"} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("unsafe/misleading %q in %q", unwanted, err)
				}
			}
		})
	}
}

func TestCloudflareErrorDiagnosticsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"code": 1000, "message": strings.Repeat("x", 10000)}}})
	}))
	defer server.Close()
	client := NewClient("token")
	client.BaseURL = server.URL
	_, err := client.Request("GET", "/zones", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limit") || len(err.Error()) > 1200 {
		t.Fatalf("expected bounded rate-limit diagnostic: %v", err)
	}
}
