package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDNSPlanPreservesExistingRecords(t *testing.T) {
	txt := dnsRecord{Type: "TXT", Name: "_amazonses.qa.info.example.com", Content: "expected"}
	cname := dnsRecord{Type: "CNAME", Name: "abc._domainkey.qa.info.example.com", Content: "abc.dkim.amazonses.com"}
	for _, tc := range []struct {
		name     string
		want     dnsRecord
		existing []dnsRecord
		action   string
		bad      bool
	}{
		{"new TXT", txt, nil, "create", false},
		{"quoted TXT", txt, []dnsRecord{{Type: "TXT", Content: `"expected"`}}, "keep", false},
		{"another TXT preserved", txt, []dnsRecord{{Type: "TXT", Content: "other"}}, "create", false},
		{"correct CNAME", cname, []dnsRecord{{Type: "CNAME", Content: "ABC.dkim.amazonses.com."}}, "keep", false},
		{"disable proxy", cname, []dnsRecord{{ID: strings.Repeat("a", 32), Type: "CNAME", Content: cname.Content, Proxied: true}}, "unproxy", false},
		{"wrong target", cname, []dnsRecord{{Type: "CNAME", Content: "other.example.com"}}, "", true},
		{"other record type", cname, []dnsRecord{{Type: "TXT", Content: "other"}}, "", true},
		{"TXT conflicts CNAME", txt, []dnsRecord{{Type: "CNAME", Content: "other"}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change, err := planDNSRecord(tc.want, tc.existing)
			if (err != nil) != tc.bad || (!tc.bad && change.Action != tc.action) {
				t.Fatal(change, err)
			}
		})
	}
}
func TestCloudflareCreateVerifyAndRerun(t *testing.T) {
	zone := strings.Repeat("a", 32)
	records := []dnsRecord{}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hidden-token" {
			t.Error("no auth")
		}
		if r.URL.Path != "/zones/"+zone+"/dns_records" {
			t.Error(r.URL.Path)
		}
		if r.Method == "POST" {
			var record dnsRecord
			json.NewDecoder(r.Body).Decode(&record)
			records = append(records, record)
			writes++
			fmt.Fprint(w, `{"success":true,"result":{}}`)
			return
		}
		selected := []dnsRecord{}
		for _, v := range records {
			if v.Name == r.URL.Query().Get("name") {
				selected = append(selected, v)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": selected, "result_info": map[string]int{"total_pages": 1}})
	}))
	defer server.Close()
	c := newCloudflareClient("hidden-token")
	c.base = server.URL
	desired := []dnsRecord{{Type: "TXT", Name: "_amazonses.qa.info.example.com", Content: "token"}, {Type: "CNAME", Name: "abc._domainkey.qa.info.example.com", Content: "abc.dkim.amazonses.com"}}
	for i := 0; i < 2; i++ {
		plan, err := c.plan(zone, desired)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.apply(zone, plan); err != nil {
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
	c := newCloudflareClient("hidden-token")
	c.base = server.URL
	_, err := c.zones("qa.info.example.com")
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
		zones := []cloudflareZone{{ID: strings.Repeat("a", 32), Name: "notexample.com", Status: "active"}, {ID: strings.Repeat("b", 32), Name: "example.com", Status: "pending"}}
		if calls == 2 {
			zones = []cloudflareZone{{ID: strings.Repeat("c", 32), Name: "example.com", Status: "active"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": zones, "result_info": map[string]int{"total_pages": 2}})
	}))
	defer server.Close()
	c := newCloudflareClient("token")
	c.base = server.URL
	zones, err := c.zones("qa.info.example.com")
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
		records := []dnsRecord{}
		if r.URL.Query().Get("page") == "2" {
			records = append(records, dnsRecord{Type: "CNAME", Name: "_amazonses.example.com", Content: "other.example.com"})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": records, "result_info": map[string]int{"total_pages": 2}})
	}))
	defer server.Close()
	c := newCloudflareClient("token")
	c.base = server.URL
	_, err := c.plan(strings.Repeat("a", 32), []dnsRecord{{Type: "TXT", Name: "_amazonses.example.com", Content: "token"}})
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
	c := newCloudflareClient("token")
	c.base = server.URL
	if _, err := c.zones("example.com"); err == nil || reached {
		t.Fatal("redirect followed", err)
	}
}
func TestCloudflarePartialWriteCanResume(t *testing.T) {
	zone := strings.Repeat("a", 32)
	desired := []dnsRecord{{Type: "TXT", Name: "_amazonses.example.com", Content: "token"}, {Type: "CNAME", Name: "abc._domainkey.example.com", Content: "abc.dkim.amazonses.com"}}
	records := []dnsRecord{}
	fail := true
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var record dnsRecord
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
		selected := []dnsRecord{}
		for _, record := range records {
			if record.Name == r.URL.Query().Get("name") {
				selected = append(selected, record)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": selected})
	}))
	defer server.Close()
	c := newCloudflareClient("token")
	c.base = server.URL
	plan, err := c.plan(zone, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.apply(zone, plan); err == nil || writes != 1 {
		t.Fatal("expected partial failure", err, writes)
	}
	fail = false
	plan, err = c.plan(zone, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.apply(zone, plan); err != nil || writes != 2 {
		t.Fatal("resume duplicated records", err, writes)
	}
}

func TestCloudflareUnproxyPatchesOnlyProxySetting(t *testing.T) {
	zone, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
	record := dnsRecord{ID: id, Type: "CNAME", Name: "abc._domainkey.example.com", Content: "abc.dkim.amazonses.com", Proxied: true}
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
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []dnsRecord{record}})
	}))
	defer server.Close()
	c := newCloudflareClient("token")
	c.base = server.URL
	desired := record
	desired.Proxied = false
	plan, err := c.plan(zone, []dnsRecord{desired})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.apply(zone, plan); err != nil || patches != 1 {
		t.Fatal(err, patches)
	}
}
