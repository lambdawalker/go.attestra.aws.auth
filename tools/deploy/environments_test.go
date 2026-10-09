package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentNamesAndDomains(t *testing.T) {
	for _, name := range []string{"dev", "qa", "prod", "demo-2"} {
		if err := validateEnvironment(name); err != nil {
			t.Fatal(err)
		}
		v, err := applicationDefaults(name, "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if v["attestra-auth-email:appOrigin"] != "https://"+name+".example.com" || v["attestra-auth-email:senderDomain"] != name+".info.example.com" || v["attestra-auth-email:senderAddress"] != "verify@"+name+".info.example.com" {
			t.Fatal(v)
		}
	}
	for _, name := range []string{"", "Prod", "qa_test", "../dev", "-qa", "qa-", "qa.example", "a\n", strings.Repeat("a", 33)} {
		if validateEnvironment(name) == nil {
			t.Fatal("accepted invalid environment", name)
		}
	}
	for _, base := range []string{"https://example.com", "example.com/path", "example.com:443", "localhost", "-invalid.com"} {
		if _, err := applicationDefaults("qa", base); err == nil {
			t.Fatal("accepted invalid domain", base)
		}
	}
}

func TestCheckpointDoesNotCrossEnvironments(t *testing.T) {
	root := t.TempDir()
	for _, env := range []string{"dev", "qa", "demo"} {
		w := bootstrapWizard{root: root, environment: env}
		if err := w.saveSelections("owner/repo", map[string]string{"PULUMI_STACK": env, "PULUMI_BACKEND_URL": "s3://state-" + env}); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []string{"dev", "qa", "demo"} {
		w := bootstrapWizard{root: root, environment: env}
		v := map[string]string{}
		if err := w.loadSelections("owner/repo", v, nil); err != nil {
			t.Fatal(err)
		}
		if v["PULUMI_STACK"] != env || v["PULUMI_BACKEND_URL"] != "s3://state-"+env {
			t.Fatal(v)
		}
	}
}

func TestLegacyCheckpointOnlyUsedForDev(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "bootstrap.local.json"), []byte(`{"Repository":"owner/repo","Stack":"dev","Backend":"s3://legacy"}`), 0600)
	for _, env := range []string{"dev", "qa"} {
		w := bootstrapWizard{root: root, environment: env}
		v := map[string]string{}
		if err := w.loadSelections("owner/repo", v, nil); err != nil {
			t.Fatal(err)
		}
		if (v["PULUMI_BACKEND_URL"] == "s3://legacy") != (env == "dev") {
			t.Fatal(env, v)
		}
	}
}

func TestOIDCSubjectUsesSelectedEnvironment(t *testing.T) {
	var m repositoryMetadata
	m.ID = 123
	m.Name = "repo"
	m.Owner.ID = 456
	m.Owner.Login = "owner"
	for _, env := range []string{"qa", "prod", "demo-2"} {
		m.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		s, err := defaultSubject(m, env)
		if err != nil || s != "repo:owner/repo:environment:"+env {
			t.Fatal(s, err)
		}
		m.CreatedAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		s, err = defaultSubject(m, env)
		if err != nil || s != "repo:owner@456/repo@123:environment:"+env {
			t.Fatal(s, err)
		}
	}
}

func TestListEnvironmentsReadsAllPagesAndStopsOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/repos/owner/repo/environments" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != fmt.Sprint(calls) {
				t.Error("wrong pagination", r.URL)
			}
			if calls == 2 && fail {
				w.WriteHeader(403)
				return
			}
			names := []map[string]string{}
			if calls == 1 {
				for i := 0; i < 100; i++ {
					names = append(names, map[string]string{"name": fmt.Sprintf("demo-%d", i)})
				}
			} else {
				names = append(names, map[string]string{"name": "qa"})
			}
			json.NewEncoder(w).Encode(map[string]any{"environments": names})
		}))
		g := newGitHubClient("test")
		g.base = server.URL
		names, err := g.listEnvironments("owner/repo")
		server.Close()
		if fail {
			if err == nil || len(names) > 0 {
				t.Fatal("silently ignored failed page")
			}
		} else if err != nil || len(names) != 101 || names[100] != "qa" {
			t.Fatal(names, err)
		}
		if calls != 2 {
			t.Fatal(calls)
		}
	}
}
func TestFreshQAStackDoesNotTouchDevConfiguration(t *testing.T) {
	o := testOptions()
	o.Stack = "qa"
	o.Root = t.TempDir()
	os.Mkdir(filepath.Join(o.Root, "infra"), 0700)
	path := filepath.Join(o.Root, "infra", "Pulumi.dev.yaml")
	os.WriteFile(path, []byte("existing dev configuration"), 0600)
	f := &bootstrapFake{list: `[{"name":"dev"}]`, state: `{"deployment":{"secrets_providers":{"type":"passphrase"}}}`, config: `{}`}
	defaults, err := applicationDefaults("qa", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = bootstrapStack(f, o, defaults, func(string) error { return nil }, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "existing dev configuration" {
		t.Fatal("modified dev")
	}
	for _, cmd := range f.calls {
		if strings.Contains(cmd, "--stack dev") || strings.Contains(cmd, "stack init dev") {
			t.Fatal(cmd)
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "stack init qa") {
		t.Fatal(f.calls)
	}
}
