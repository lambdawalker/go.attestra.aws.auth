package workflow

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
)

type nukeFakeAWS struct {
	call func(any, ...string) (bool, error)
}

func (f nukeFakeAWS) Call(result any, args ...string) (bool, error) { return f.call(result, args...) }

func TestNukeRecoveryMissingBucketsAndKeysSucceed(t *testing.T) {
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Account: "123456789012", Backends: []nukeBackend{{"gone", "us-east-2"}}, Keys: []nukeKey{{"missing-key", "us-east-2"}}}}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(_ any, args ...string) (bool, error) {
			if args[0] == "kms" {
				return false, &awsenv.Error{Code: "NotFoundException"}
			}
			return false, &awsenv.Error{Code: "NoSuchBucket"}
		}}
	}
	n.cleanupRecovery()
	if !nukeSuccessful(n.receipt.Results) {
		t.Fatal(n.receipt.Results)
	}
}

func TestNukeRecoveryAccessDeniedPreservesAllRecoveryArtifacts(t *testing.T) {
	mutations := 0
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Account: "123456789012", Backends: []nukeBackend{{"denied", "us-east-2"}}, Keys: []nukeKey{{"key", "us-east-2"}}}}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(_ any, args ...string) (bool, error) {
			if strings.Contains(strings.Join(args, " "), "delete") {
				mutations++
			}
			return false, &awsenv.Error{Code: "AccessDenied"}
		}}
	}
	n.cleanupRecovery()
	if mutations != 0 || nukeSuccessful(n.receipt.Results) {
		t.Fatalf("mutations=%d report=%+v", mutations, n.receipt.Results)
	}
}

func TestNukeRecoveryDoesNotScheduleSharedKey(t *testing.T) {
	// Earlier read-only preflight fails closed; no KMS mutation is possible.
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Account: "123456789012", Keys: []nukeKey{{"key", "us-east-2"}}}}
	scheduled := false
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(result any, args ...string) (bool, error) {
			if args[1] == "schedule-key-deletion" {
				scheduled = true
				return true, nil
			}
			if result != nil {
				_ = json.Unmarshal([]byte(`{"KeyMetadata":{"Arn":"arn:aws:kms:us-east-2:123456789012:key/key","KeyManager":"CUSTOMER","KeyState":"Enabled"}}`), result)
			}
			return true, nil
		}}
	}
	n.confirmKey = func(string, string) error { return errors.New("shared key ownership not confirmed") }
	n.cleanupRecovery()
	if scheduled || nukeSuccessful(n.receipt.Results) {
		t.Fatal("key scheduled despite failed exclusivity confirmation")
	}
}

func TestNukeRecoveryObjectScope(t *testing.T) {
	if err := nukeRecoveryObjectScope([]string{".pulumi/meta.yaml", ".pulumi/history/attestra-auth-email/dev/1.json", "bootstrap-ready.json"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"customer-backup.zip", ".pulumi/stacks/another-project/dev.json", ".pulumi/history/another-project/dev/1.json"} {
		if nukeRecoveryObjectScope([]string{key}) == nil {
			t.Fatal("accepted unrelated recovery object " + key)
		}
	}
}

func TestNukeKMSOnlyAfterEveryBucketIsVerifiedGone(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "bucket-denied"}[failDelete], func(t *testing.T) {
			gone, scheduled := false, false
			n := &nukeWorkflow{root: t.TempDir(), path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Account: "123456789012", Backends: []nukeBackend{{"state", "us-east-2"}}}}
			n.runnerForTarget = func(nukeTarget) commandRunner {
				return nukeRunnerFunc(func(string, bool, string, ...string) ([]byte, error) { return []byte(`[]`), nil })
			}
			n.confirmKey = func(string, string) error { return nil }
			n.awsForRegion = func(string) awsenv.API {
				return nukeFakeAWS{func(result any, args ...string) (bool, error) {
					body := `{}`
					switch args[0] + " " + args[1] {
					case "s3api head-bucket":
						if gone {
							return false, &awsenv.Error{Code: "NoSuchBucket"}
						}
					case "s3api list-object-versions":
						body = `{"Versions":[],"DeleteMarkers":[]}`
					case "s3api get-bucket-encryption":
						body = `{"ServerSideEncryptionConfiguration":{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms","KMSMasterKeyID":"key"}}]}}`
					case "s3api delete-bucket":
						if failDelete {
							return false, &awsenv.Error{Code: "AccessDenied"}
						}
						gone = true
					case "kms describe-key":
						body = `{"KeyMetadata":{"Arn":"arn:aws:kms:us-east-2:123456789012:key/key","KeyManager":"CUSTOMER","KeyState":"Enabled"}}`
						if scheduled {
							body = strings.Replace(body, `"Enabled"`, `"PendingDeletion"`, 1)
						}
					case "kms schedule-key-deletion":
						if !gone {
							t.Fatal("key scheduled before bucket absence")
						}
						scheduled = true
					default:
						t.Fatalf("unexpected operation: %v", args)
					}
					if result != nil {
						if err := json.Unmarshal([]byte(body), result); err != nil {
							t.Fatal(err)
						}
					}
					return true, nil
				}}
			}
			n.cleanupRecovery()
			if scheduled == failDelete || nukeSuccessful(n.receipt.Results) == failDelete {
				t.Fatalf("gone=%t scheduled=%t results=%+v", gone, scheduled, n.receipt.Results)
			}
		})
	}
}

func TestNukeStateKeyInventoryKeepsOnlyKeyIdentity(t *testing.T) {
	n := &nukeWorkflow{}
	data := []byte(`{"deployment":{"secrets_providers":{"type":"cloud","state":{"url":"awskms://alias/attestra?region=us-west-2","encryptedkey":"do-not-persist"}}}}`)
	if err := n.captureStateKey(data, "us-east-2"); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(n.receipt)
	if len(n.receipt.Keys) != 1 || n.receipt.Keys[0].Region != "us-west-2" || n.receipt.Keys[0].ID != "alias/attestra" || strings.Contains(string(b), "do-not-persist") {
		t.Fatal(string(b))
	}
}

func TestNukeReportWriteFailureBlocksRecovery(t *testing.T) {
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "missing", "report.json")}
	n.record(nukeResult{"item", "ABSENT", "gone"})
	n.awsForRegion = func(string) awsenv.API {
		t.Fatal("recovery must not start after report persistence failure")
		return nil
	}
	n.cleanupRecovery()
	if n.persistErr == nil {
		t.Fatal("report loss was ignored")
	}
}
