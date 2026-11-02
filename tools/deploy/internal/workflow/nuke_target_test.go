package workflow

import (
	"encoding/json"
	"errors"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"path/filepath"
	"strings"
	"testing"
)

type nukeRunnerFunc func(string, bool, string, ...string) ([]byte, error)

func (f nukeRunnerFunc) Exec(dir string, capture bool, name string, args ...string) ([]byte, error) {
	return f(dir, capture, name, args...)
}

func TestNukePulumiFailureStillChecksSavedInventoryAndDNS(t *testing.T) {
	n := &nukeWorkflow{root: t.TempDir(), path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Account: "123456789012"}}
	n.runnerForTarget = func(nukeTarget) commandRunner {
		return nukeRunnerFunc(func(string, bool, string, ...string) ([]byte, error) { return nil, errors.New("state unavailable") })
	}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(result any, args ...string) (bool, error) {
			switch args[0] {
			case "s3api":
				return false, &awsenv.Error{Code: "AccessDenied"}
			case "lambda":
				return false, &awsenv.Error{Code: "ResourceNotFoundException"}
			case "logs":
				_ = json.Unmarshal([]byte(`{"logGroups":[]}`), result)
				return true, nil
			}
			t.Fatal(args)
			return false, nil
		}}
	}
	target := nukeTarget{Backend: "s3://state", Region: "us-east-2", Project: "attestra-auth-email", Stack: "dev", Evidence: teardownProgress{InventoryCaptured: true, Inventory: []teardownProbe{{Name: "fn", Kind: "lambda", ID: "fn"}}, Desired: []dns.Record{{Name: "dev.example.com", Type: "CNAME", Content: "old.example.net"}}}}
	n.destroyTarget(&target)
	seenLog, seenDNS := false, false
	for _, r := range n.receipt.Results {
		seenLog = seenLog || r.Name == "Log group /aws/lambda/fn"
		seenDNS = seenDNS || r.Name == "DNS CNAME dev.example.com"
	}
	if !seenLog || !seenDNS {
		t.Fatalf("independent cleanup was skipped: %+v", n.receipt.Results)
	}
}

func TestNukeStateRejectsOtherProjectsAccountsAndProviderOverrides(t *testing.T) {
	valid := `{"deployment":{"resources":[{"urn":"urn:pulumi:dev::attestra-auth-email::aws:s3/bucketV2:BucketV2::data","type":"aws:s3/bucketV2:BucketV2","id":"data"}]}}`
	if err := validateNukeState([]byte(valid), "attestra-auth-email", "dev", "123456789012"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.ReplaceAll(valid, "attestra-auth-email", "another-project"), strings.ReplaceAll(valid, `"id":"data"`, `"id":"arn:aws:s3:us-east-2:999999999999:data"`), `{"deployment":{"resources":[{"urn":"urn:pulumi:dev::attestra-auth-email::pulumi:providers:aws::default","type":"pulumi:providers:aws","inputs":{"profile":"other-account"}}]}}`} {
		if validateNukeState([]byte(bad), "attestra-auth-email", "dev", "123456789012") == nil {
			t.Fatal("accepted unrelated resource/provider")
		}
	}
}

func TestNukeDeletionGuardsPreserveEncryptedStateAndExternalResources(t *testing.T) {
	data := []byte(`{"version":3,"deployment":{"secrets_providers":{"type":"passphrase","state":{"salt":"encrypted"}},"resources":[{"urn":"managed","protect":true,"retainOnDelete":true,"outputs":{"secret":{"ciphertext":"keep"}}},{"urn":"external","external":true,"protect":true,"retainOnDelete":true}]}}`)
	b, changed, err := nukeDestroyableExport(data)
	if err != nil || !changed {
		t.Fatal(err)
	}
	var state struct {
		Deployment struct {
			Resources []struct {
				Protect, RetainOnDelete bool
				Outputs                 map[string]json.RawMessage
			}
			Secrets map[string]json.RawMessage `json:"secrets_providers"`
		}
	}
	if json.Unmarshal(b, &state) != nil || state.Deployment.Resources[0].Protect || state.Deployment.Resources[0].RetainOnDelete || !state.Deployment.Resources[1].Protect || !strings.Contains(string(b), `"ciphertext":"keep"`) {
		t.Fatal(string(b))
	}
}

func TestNukeInventoryIncludesAdminDomainAndBranding(t *testing.T) {
	data := []byte(`{"deployment":{"resources":[{"urn":"domain","type":"aws:cognito/userPoolDomain:UserPoolDomain","id":"admin-login","outputs":{"userPoolId":"pool"}},{"urn":"branding","type":"aws:cognito/managedLoginBranding:ManagedLoginBranding","id":"branding-id","outputs":{"userPoolId":"pool"}}]}}`)
	p := teardownProgress{}
	if err := captureTeardownInventory(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Inventory[0].Kind != "cognito-domain" || p.Inventory[1].Kind != "pool" || p.Inventory[1].ID != "pool" {
		t.Fatal(p.Inventory)
	}
}

func TestNukeDNSDoesNotMisclassifyRoute53AliasAsCloudflare(t *testing.T) {
	data := []byte(`{"deployment":{"resources":[{"urn":"urn::app-associations-cdn","type":"aws:cloudfront/distribution:Distribution","id":"CDN","outputs":{"aliases":["app.example.com"],"domainName":"cdn.example.net"}},{"urn":"record","type":"aws:route53/record:Record","id":"record","outputs":{"name":"app.example.com","type":"A","zoneId":"Z"}}]}}`)
	p := teardownProgress{}
	if err := captureNukeDNS(data, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Desired) != 0 {
		t.Fatalf("Route53 alias incorrectly sent to Cloudflare: %+v", p.Desired)
	}
}
