package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

// Import only public associations. Empty input leaves any existing website alone.
func (w *bootstrapWizard) associationDefaults(current string, defaults map[string]string) error {
	if current != "" {
		var apps []assetlinks.Application
		if json.Unmarshal([]byte(current), &apps) != nil {
			return errors.New("invalid androidAssociations JSON")
		}
		_, e := assetlinks.Render(apps)
		return e
	}
	if _, known := w.remembered("androidAssociationsSkipped"); known {
		return nil
	}
	path := ""
	if err := ui.Input("Android association JSON file (blank to keep website hosting unchanged)", &path, false, false).Description("Public package names/fingerprints only. Opting in hosts appOrigin on CloudFront; do not use for a hostname with an existing website. See docs/android-associations.md.").Run(); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return w.remember("androidAssociationsSkipped", "true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var apps []assetlinks.Application
	if err = json.Unmarshal(data, &apps); err != nil {
		return err
	}
	if _, err = assetlinks.Render(apps); err != nil {
		return err
	}
	compact, _ := json.Marshal(apps)
	defaults["attestra-auth-email:androidAssociations"] = string(compact)
	return nil
}
func associationTargets(stack string, route53 bool) []string {
	prefix := "urn:pulumi:" + stack + "::attestra-auth-email::"
	targets := []string{prefix + "pulumi:providers:aws::app-edge", prefix + "aws:acm/certificate:Certificate::app-certificate"}
	if route53 {
		targets = append(targets, prefix+"aws:route53/record:Record::app-certificate-dns")
	}
	return targets
}
func checkAssetLinks(client *http.Client, endpoint, expected string) error {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/.well-known/assetlinks.json" {
		return errors.New("invalid asset links URL")
	}
	probe := *client
	probe.Timeout = 15 * time.Second
	probe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := probe.Get(endpoint)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || media != "application/json" {
		return fmt.Errorf("asset links require HTTP 200 application/json without redirects (got %d %s)", response.StatusCode, media)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 1_048_577))
	if e != nil {
		return e
	}
	if len(data) > 1_048_576 {
		return errors.New("oversized asset links document")
	}
	var actual, want any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(actual, want) {
		return errors.New("public asset links differ from approved deployed associations")
	}
	return nil
}
func waitAssetLinks(r commandRunner, o options) error {
	data, e := r.Exec(filepath.Join(o.Root, "infra"), true, "pulumi", "stack", "output", "--json", "--stack", o.Stack)
	if e != nil {
		return e
	}
	var out struct {
		URL     string `json:"assetLinksUrl"`
		Content string `json:"assetLinksContent"`
	}
	if json.Unmarshal(data, &out) != nil {
		return errors.New("invalid stack outputs")
	}
	if out.URL == "" {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		err := checkAssetLinks(&http.Client{}, out.URL, out.Content)
		if err == nil {
			fmt.Println("✓ Android associations ready: " + out.URL)
			return nil
		}
		fmt.Printf("Waiting for Android association hosting: %v; checking again in 10 seconds.\n", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("Android association readiness stopped; resources retained: %w", err)
		case <-time.After(10 * time.Second):
		}
	}
}
