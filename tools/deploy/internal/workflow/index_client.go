package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	envregistry "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/registry"

	"github.com/lambdawalker/go.attestra.aws.auth/registry"
)

func indexClientFromConfig(r *processRunner, dir, stack string) (*envregistry.Client, error) {
	data, e := r.Exec(dir, true, "pulumi", "config", "--json", "--stack", stack)
	if e != nil {
		return nil, e
	}
	var cfg map[string]struct{ Value string }
	if json.Unmarshal(data, &cfg) != nil {
		return nil, errors.New("invalid registry configuration")
	}
	address := cfg["attestra-auth-email:indexApiUrl"].Value
	if address == "" {
		return nil, nil
	}
	return newIndexClient(r.env, address, cfg["attestra-auth-email:indexRegion"].Value, stack)
}
func newIndexClient(env []string, address, region, stack string) (*envregistry.Client, error) {
	c := &envregistry.Client{URL: address, Region: region, Environment: stack, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, v := range env {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.ToUpper(parts[0]) {
		case sourceEnvironmentKey:
			source, err := decodeCredentialSource(parts[1])
			if err != nil {
				return nil, err
			}
			c.Provider = source.provider()
		case "AWS_ACCESS_KEY_ID":
			c.Credentials.AccessKeyID = parts[1]
		case "AWS_SECRET_ACCESS_KEY":
			c.Credentials.SecretAccessKey = parts[1]
		case "AWS_SESSION_TOKEN":
			c.Credentials.SessionToken = parts[1]
		}
	}
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !regexp.MustCompile(`^[a-z0-9]+\.execute-api\.`+regexp.QuoteMeta(c.Region)+`\.amazonaws\.com$`).MatchString(u.Host) || (c.Provider == nil && (c.Credentials.AccessKeyID == "" || c.Credentials.SecretAccessKey == "")) || !registry.ValidEnvironment(stack) {
		return nil, errors.New("invalid registry endpoint or AWS signing credentials; rerun setup")
	}
	return c, nil
}

type pendingIndex struct {
	API, Region, Environment string
	Receipt                  registry.Receipt
	Config                   registry.Configuration
	Ready                    bool
	Deployed                 bool
}

func indexPendingPath(root, stack string) string {
	return filepath.Join(root, ".attestra", "index-publication."+stack+".local.json")
}
func publicIndexConfiguration(r commandRunner, o options) (registry.Configuration, error) {
	data, e := r.Exec(filepath.Join(o.Root, "infra"), true, "pulumi", "stack", "output", "--json", "--stack", o.Stack)
	var result registry.Configuration
	if e != nil {
		return result, e
	}
	var outputs struct {
		APIURL  string `json:"apiUrl"`
		Pool    string `json:"userPoolId"`
		Client  string `json:"clientId"`
		Capture bool   `json:"captureEnabled"`
	}
	if json.Unmarshal(data, &outputs) != nil {
		return result, errors.New("invalid application outputs")
	}
	result.APIURL, result.AWSRegion, result.CognitoUserPoolID, result.CognitoClientID = outputs.APIURL, o.Region, outputs.Pool, outputs.Client
	result.Features.IDCapture = outputs.Capture
	return result, registry.Validate(result)
}

func savePendingIndex(path string, p pendingIndex) error {
	data, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if e = os.WriteFile(path+".tmp", data, 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func (r *processRunner) withIndex(o options, work func() error) error {
	c, e := indexClientFromConfig(r, filepath.Join(o.Root, "infra"), o.Stack)
	if e != nil {
		return e
	}
	if c == nil {
		if work == nil {
			return errors.New("registry not configured; run setup first")
		}
		return work()
	}
	return r.withIndexClient(o, c, work)
}
func (r *processRunner) withIndexClient(o options, c *envregistry.Client, work func() error) error {
	path := indexPendingPath(o.Root, o.Stack)
	lock, e := os.OpenFile(path+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("another local index/deployment operation is active; inspect the publication .lock file before removing a stale lock")
	}
	lock.Close()
	defer os.Remove(path + ".lock")
	if data, e := os.ReadFile(path); e == nil {
		var pending pendingIndex
		if json.Unmarshal(data, &pending) != nil || pending.API != c.URL || pending.Environment != o.Stack || pending.Region != c.Region {
			return errors.New("pending registry receipt does not match this environment; inspect it before continuing")
		}
		if o.ReleaseIndex {
			if err := typedConfirmation("After verifying no deployment is running, type the environment to release its saved index lock", o.Stack); err != nil {
				return err
			}
			if pending.Receipt.Revision == 0 {
				receipt, err := c.Change(registry.Change{Operation: "begin", Token: pending.Receipt.Token})
				if err != nil {
					return err
				}
				pending.Receipt = receipt
				if err = savePendingIndex(path, pending); err != nil {
					return err
				}
			}
			if _, err := c.Change(registry.Change{Operation: "abandon", Token: pending.Receipt.Token, Revision: pending.Receipt.Revision}); err != nil {
				return err
			}
			return os.Remove(path)
		}
		if !pending.Deployed && r.indexLease != nil && pending.Receipt == r.indexLease.Receipt && work != nil {
			r.indexLease = nil
			return r.performIndexWork(o, c, path, pending, work)
		}
		if !pending.Deployed {
			return errors.New("interrupted deployment holds a registry lock; inspect Pulumi and the pending receipt before explicitly releasing it; see docs/environment-index.md")
		}
		if !pending.Ready {
			pending.Config, e = publicIndexConfiguration(r, o)
			if e != nil {
				return e
			}
			pending.Ready = true
			if e = savePendingIndex(path, pending); e != nil {
				return e
			}
		}
		if _, e = c.Change(registry.Change{Operation: "publish", Token: pending.Receipt.Token, Revision: pending.Receipt.Revision, Config: pending.Config}); e != nil {
			return e
		}
		if e = os.Remove(path); e != nil {
			return e
		}
		fmt.Println("✓ Pending environment index publication completed.")
		if work == nil {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if work == nil || o.ReleaseIndex {
		return errors.New("no pending publication; no changes made")
	}
	token := make([]byte, 24)
	if _, e = rand.Read(token); e != nil {
		return e
	}
	pending := pendingIndex{API: c.URL, Region: c.Region, Environment: o.Stack, Receipt: registry.Receipt{Token: hex.EncodeToString(token)}}
	if e = savePendingIndex(path, pending); e != nil {
		return e
	}
	receipt, e := c.Change(registry.Change{Operation: "begin", Token: pending.Receipt.Token})
	if e != nil {
		if errors.Is(e, envregistry.ErrConflict) {
			os.Remove(path)
			return e
		}
		return fmt.Errorf("registry lease was not acquired; pending request retained for inspection: %w", e)
	}
	pending.Receipt = receipt
	if e = savePendingIndex(path, pending); e != nil {
		return e
	}
	return r.performIndexWork(o, c, path, pending, work)
}
func (r *processRunner) performIndexWork(o options, c *envregistry.Client, path string, pending pendingIndex, work func() error) error {
	receipt := pending.Receipt
	var e error
	if e = work(); e != nil {
		if _, releaseErr := c.Change(registry.Change{Operation: "abandon", Token: receipt.Token, Revision: receipt.Revision}); releaseErr == nil {
			os.Remove(path)
		} else {
			fmt.Println("Registry lock retained; see pending publication receipt and recovery documentation.")
		}
		return e
	}
	pending.Deployed = true
	if e = savePendingIndex(path, pending); e != nil {
		return e
	}
	pending.Config, e = publicIndexConfiguration(r, o)
	if e == nil {
		pending.Ready = true
		e = savePendingIndex(path, pending)
	}
	if e == nil {
		_, e = c.Change(registry.Change{Operation: "publish", Token: receipt.Token, Revision: receipt.Revision, Config: pending.Config})
	}
	if e != nil {
		return fmt.Errorf("deployment succeeded; index publication pending. Retry with deploy -publish-index using the same checkout/receipt: %w", e)
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	fmt.Println("✓ Environment index synchronized.")
	return nil
}

// Reserve the environment before setup modifies IAM/GitHub or deploys the app.
func (r *processRunner) acquireSetupIndex(o options, c *envregistry.Client) error {
	if p := r.indexLease; p != nil {
		if p.API != c.URL || p.Region != c.Region || p.Environment != o.Stack {
			return errors.New("shared registry differs from the active setup lock; inspect configuration before retrying")
		}
		return nil
	}
	path := indexPendingPath(o.Root, o.Stack)
	if _, err := os.Stat(path); err == nil {
		if err = r.withIndexClient(o, c, nil); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	lock, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("another local index operation is active; inspect its lock before retrying")
	}
	lock.Close()
	defer os.Remove(path + ".lock")
	token := make([]byte, 24)
	if _, e = rand.Read(token); e != nil {
		return e
	}
	pending := pendingIndex{API: c.URL, Region: c.Region, Environment: o.Stack, Receipt: registry.Receipt{Token: hex.EncodeToString(token)}}
	if e = savePendingIndex(path, pending); e != nil {
		return e
	}
	receipt, e := c.Change(registry.Change{Operation: "begin", Token: pending.Receipt.Token})
	if e != nil {
		if errors.Is(e, envregistry.ErrConflict) {
			os.Remove(path)
		}
		return e
	}
	pending.Receipt = receipt
	r.indexLease = &pending
	return savePendingIndex(path, pending)
}
func (r *processRunner) releaseSetupIndex(root string) {
	p := r.indexLease
	if p == nil {
		return
	}
	r.indexLease = nil
	c, e := newIndexClient(r.env, p.API, p.Region, p.Environment)
	if e == nil {
		_, e = c.Change(registry.Change{Operation: "abandon", Token: p.Receipt.Token, Revision: p.Receipt.Revision})
	}
	if e == nil {
		os.Remove(indexPendingPath(root, p.Environment))
	} else {
		fmt.Println("Setup lock retained; recover using the pending index receipt before deploying again.")
	}
}
