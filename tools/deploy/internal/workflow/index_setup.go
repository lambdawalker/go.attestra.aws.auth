package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/build"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func indexSourceHash(root string) (string, error) {
	files := []string{"go.mod", "go.sum"}
	for _, dir := range []string{"registry", "cmd/index", "infra-index"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".go") || d.Name() == "go.mod" || d.Name() == "go.sum" || d.Name() == "Pulumi.yaml") {
				rel, _ := filepath.Rel(root, path)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(files)
	h := sha256.New()
	for _, name := range files {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			return "", e
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func buildIndex(root string) error {
	dir, e := os.MkdirTemp("", "attestra-index-build-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	binary := filepath.Join(dir, "bootstrap")
	cmd := exec.Command("go", "build", "-trimpath", "-tags", "lambda.norpc", "-o", binary, "./cmd/index")
	cmd.Dir = root
	cmd.Env = append(awsenv.WithoutCredentials(os.Environ()), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Join(root, "dist"), 0755); e != nil {
		return e
	}
	return build.PackageLambda(binary, filepath.Join(root, "dist", "index.zip"))
}
func replaceEnvironment(env []string, values map[string]string) []string {
	result := []string{}
	for _, v := range env {
		k := strings.SplitN(v, "=", 2)[0]
		if _, ok := values[strings.ToUpper(k)]; !ok {
			result = append(result, v)
		}
	}
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	return result
}

// A separate state bucket and conditional S3 lock serialize shared infrastructure
// setup across machines. A crashed bootstrap is recovered explicitly, never stolen.
func (w *bootstrapWizard) prepareIndex(base, zone string) error {
	fmt.Println("Shared environment index • independent Pulumi project")
	o := w.o
	o.Stack = "shared"
	o.Backend = "s3://attestra-index-state-" + w.account + "-" + o.Region
	if e := ensureStateBucket(w.a, o, w.account); e != nil {
		var ae *awsenv.Error
		if !errors.As(e, &ae) || ae.Code != "BucketAlreadyOwnedByYou" {
			return e
		}
		if e = ensureStateBucket(w.a, o, w.account); e != nil {
			return e
		}
	}
	bucket := strings.TrimPrefix(o.Backend, "s3://")
	body, e := os.CreateTemp("", "attestra-index-lock-")
	if e != nil {
		return e
	}
	name := body.Name()
	defer os.Remove(name)
	if e = json.NewEncoder(body).Encode(map[string]string{"environment": w.environment, "startedAt": time.Now().UTC().Format(time.RFC3339Nano)}); e != nil {
		body.Close()
		return e
	}
	if e = body.Close(); e != nil {
		return e
	}
	var lock struct{ ETag string }
	if _, e = w.a.Call(&lock, "s3api", "put-object", "--bucket", bucket, "--key", "bootstrap.lock", "--body", name, "--if-none-match", "*", "--expected-bucket-owner", w.account); e != nil {
		return fmt.Errorf("shared index setup is locked or inaccessible; inspect s3://%s/bootstrap.lock before retrying: %w", bucket, e)
	}
	if lock.ETag == "" {
		return errors.New("shared index bootstrap lock response missing ETag; inspect the lock before retrying")
	}
	defer func() {
		if _, e := w.a.Call(nil, "s3api", "delete-object", "--bucket", bucket, "--key", "bootstrap.lock", "--if-match", lock.ETag, "--expected-bucket-owner", w.account); e != nil {
			fmt.Println("Could not remove shared index bootstrap.lock; inspect it before the next setup.")
		}
	}()
	if e = checkIndexTeardownPending(w.a, bucket, w.account); e != nil {
		return e
	}
	if e = archiveCompletedIndexTeardown(w.root, w.account, o.Region); e != nil {
		return e
	}
	if w.secrets.IndexPulumi == "" {
		if e = ui.Input("Shared index Pulumi passphrase (use the original on reruns; save it in your password manager)", &w.secrets.IndexPulumi, true, true).Run(); e != nil {
			return e
		}
	}
	r := &processRunner{onPreview: w.recordPreview, env: replaceEnvironment(w.r.env, map[string]string{"PULUMI_BACKEND_URL": o.backendURL(), "PULUMI_CONFIG_PASSPHRASE": w.secrets.IndexPulumi})}
	dir := filepath.Join(w.root, "infra-index")
	run := func(capture bool, args ...string) ([]byte, error) { return r.Exec(dir, capture, "pulumi", args...) }
	list, e := run(true, "stack", "ls", "--json", "--non-interactive")
	if e != nil {
		return e
	}
	var stacks []struct{ Name string }
	if json.Unmarshal(list, &stacks) != nil || stacks == nil {
		return errors.New("invalid shared stack list")
	}
	exists := false
	for _, s := range stacks {
		if s.Name == "shared" || strings.HasSuffix(s.Name, "/attestra-index/shared") {
			exists = true
		}
	}
	if !exists {
		matches, _ := filepath.Glob(filepath.Join(dir, "Pulumi.shared.yaml*"))
		if len(matches) > 0 {
			return errors.New("shared index state missing but local configuration exists; restore its original backend before continuing")
		}
		repeat := ""
		if e = ui.Input("Confirm new shared index Pulumi passphrase", &repeat, true, true).Run(); e != nil {
			return e
		}
		if repeat != w.secrets.IndexPulumi {
			return errors.New("shared index passphrases do not match")
		}
		if _, e = run(false, "stack", "init", "shared", "--secrets-provider", "passphrase", "--non-interactive"); e != nil {
			return e
		}
	} else {
		if _, e = run(true, "stack", "select", "shared", "--non-interactive"); e != nil {
			return e
		}
		if _, e = run(true, "config", "refresh", "--stack", "shared", "--force", "--non-interactive"); e != nil {
			return e
		}
	}
	data, e := run(true, "config", "--json", "--stack", "shared")
	if e != nil {
		return e
	}
	var cfg map[string]struct{ Value string }
	if json.Unmarshal(data, &cfg) != nil {
		return errors.New("invalid index configuration")
	}
	for k, v := range cfg {
		if strings.HasPrefix(k, "aws:") && (k != "aws:region" || v.Value != o.Region) {
			return errors.New("shared index AWS provider configuration requires review")
		}
	}
	domain := "index." + base
	if v := cfg["attestra-index:domain"].Value; v != "" {
		domain = v
		zone = cfg["attestra-index:route53ZoneId"].Value
	}
	if e := validateBootstrapApp("https://"+domain, domain, "verify@"+domain); e != nil {
		return fmt.Errorf("invalid shared index domain: %w", e)
	}
	if zone == "" {
		if e = w.ensureCloudflare(); e != nil {
			return e
		}
	}
	if e = w.offerCredentialSaving(); e != nil {
		return e
	}
	hash, e := indexSourceHash(w.root)
	if e != nil {
		return e
	}
	for k, v := range map[string]string{"aws:region": o.Region, "attestra-index:domain": domain, "attestra-index:route53ZoneId": zone, "attestra-index:sourceHash": hash} {
		if cfg[k].Value == v {
			continue
		}
		if _, e = run(false, "config", "set", k, "--stack", "shared", "--non-interactive", "--", v); e != nil {
			return e
		}
	}
	marker := filepath.Join(filepath.Dir(name), filepath.Base(name)+"-ready")
	defer os.Remove(marker)
	_, markerErr := w.a.Call(nil, "s3api", "get-object", "--bucket", bucket, "--key", "bootstrap-ready.json", "--expected-bucket-owner", w.account, marker)
	ready := map[string]string{}
	if markerErr == nil {
		b, _ := os.ReadFile(marker)
		_ = json.Unmarshal(b, &ready)
	} else {
		var ae *awsenv.Error
		if !errors.As(markerErr, &ae) || (ae.Code != "NoSuchKey" && ae.Code != "404") {
			return markerErr
		}
	}
	if ready["sourceHash"] != hash || ready["domain"] != domain || ready["zone"] != zone {
		if e = buildIndex(w.root); e != nil {
			return e
		}
		targets := []string{"--target", "urn:pulumi:shared::attestra-index::aws:acm/certificate:Certificate::index-certificate"}
		if zone != "" {
			targets = append(targets, "--target", "urn:pulumi:shared::attestra-index::aws:route53/record:Record::index-certificate-dns")
		}
		for _, op := range []string{"preview", "up"} {
			args := []string{op, "--stack", "shared", "--non-interactive"}
			if op == "up" {
				args = append(args, "--yes")
			}
			args = append(args, targets...)
			if _, e = run(false, args...); e != nil {
				return e
			}
		}
		arn, e := run(true, "stack", "output", "certificateArn", "--stack", "shared")
		if e != nil {
			return e
		}
		if e = w.waitCertificate(domain, strings.TrimSpace(string(arn)), zone != ""); e != nil {
			return e
		}
		for _, op := range []string{"preview", "up"} {
			args := []string{op, "--stack", "shared", "--non-interactive"}
			if op == "up" {
				args = append(args, "--yes")
			}
			if _, e = run(false, args...); e != nil {
				return e
			}
		}
	} else {
		fmt.Println("✓ Shared index infrastructure already configured for this source version.")
	}
	outputs, e := run(true, "stack", "output", "--json", "--stack", "shared")
	if e != nil {
		return e
	}
	var out map[string]string
	if json.Unmarshal(outputs, &out) != nil || out["apiUrl"] == "" || out["domainTarget"] == "" || out["apiArn"] == "" {
		return errors.New("shared index outputs are incomplete")
	}
	if _, e = newIndexClient(w.r.env, out["apiUrl"], o.Region, w.environment); e != nil {
		return e
	}
	if out["indexUrl"] != "https://"+domain+"/v1/environments" {
		return errors.New("shared index URL differs from its configured domain")
	}
	if zone == "" {
		if e = w.publishDNS(domain, []dns.Record{{Type: "CNAME", Name: domain, Content: out["domainTarget"]}}); e != nil {
			return e
		}
	}
	if e = waitIndexHTTP(out["apiUrl"] + "/v1/environments"); e != nil {
		return e
	}
	if e = waitIndexHTTP(out["indexUrl"]); e != nil {
		return e
	}
	b, _ := json.Marshal(map[string]string{"sourceHash": hash, "domain": domain, "zone": zone})
	if e = os.WriteFile(marker, b, 0600); e != nil {
		return e
	}
	if _, e = w.a.Call(nil, "s3api", "put-object", "--bucket", bucket, "--key", "bootstrap-ready.json", "--body", marker, "--expected-bucket-owner", w.account); e != nil {
		return e
	}
	client, e := newIndexClient(w.r.env, out["apiUrl"], o.Region, w.environment)
	if e != nil {
		return e
	}
	if e = w.r.acquireSetupIndex(w.o, client); e != nil {
		return e
	}
	for k, v := range map[string]string{"indexApiUrl": out["apiUrl"], "indexRegion": o.Region, "indexApiArn": out["apiArn"], "indexUrl": out["indexUrl"]} {
		if _, e = w.r.Exec(filepath.Join(w.root, "infra"), false, "pulumi", "config", "set", "attestra-auth-email:"+k, "--stack", w.o.Stack, "--non-interactive", "--", v); e != nil {
			return e
		}
	}
	w.indexAPIArn = out["apiArn"]
	if w.memory.Summary.Domains == nil {
		w.memory.Summary.Domains = map[string]string{}
	}
	w.memory.Summary.Domains["Index"] = out["indexUrl"]
	fmt.Println("✓ Shared index ready:", out["indexUrl"])
	return nil
}
func indexPublicationPolicy(arn, environment string) map[string]any {
	return map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": "execute-api:Invoke", "Resource": arn + "/*/POST/v1/environments/" + environment + "/changes"}}}
}

func waitIndexHTTP(address string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return waitIndexHTTPContext(ctx, address, client, 5*time.Second, os.Stdout)
}

func waitIndexHTTPContext(ctx context.Context, address string, client *http.Client, interval time.Duration, output io.Writer) error {
	fmt.Fprintf(output, "Checking shared index endpoint: %s\n", address)
	lastFailure := "no response received"
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				lastFailure = err.Error()
			}
		} else {
			var body struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&body)
			res.Body.Close()
			switch {
			case res.StatusCode != http.StatusOK:
				lastFailure = fmt.Sprintf("HTTP %d", res.StatusCode)
			case decodeErr != nil:
				lastFailure = "HTTP 200 with invalid index JSON"
			case body.SchemaVersion != 1:
				lastFailure = fmt.Sprintf("HTTP 200 with unsupported schemaVersion %d", body.SchemaVersion)
			default:
				if ctx.Err() == nil {
					fmt.Fprintf(output, "✓ Shared index endpoint ready: %s\n", address)
					return nil
				}
			}
		}
		if ctx.Err() != nil {
			return fmt.Errorf("shared index readiness wait stopped for %s: %w; last failure: %s; infrastructure retained, rerun setup to resume", address, ctx.Err(), lastFailure)
		}
		fmt.Fprintf(output, "Waiting for shared index %s: %s; checking again in %s.\n", address, lastFailure, interval)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("shared index readiness wait stopped for %s: %w; last failure: %s; infrastructure retained, rerun setup to resume", address, ctx.Err(), lastFailure)
		case <-timer.C:
		}
	}
}
