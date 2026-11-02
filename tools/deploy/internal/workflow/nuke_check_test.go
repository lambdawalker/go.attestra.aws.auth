package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
)

func TestPostNukeCheckRechecksLastReportWithoutDeleting(t *testing.T) {
	backup := "arn:aws:dynamodb:us-east-2:123456789012:table/data/backup/123"
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{
		Account: "123456789012", Results: []nukeResult{
			{"Backup " + backup, "FAILED", "old failure"},
			{"Deployment role arn:aws:iam::123456789012:role/deploy", "ABSENT", "old absence"},
			{"Deployment role arn:aws:iam::123456789012:role/deploy", "FAILED", "duplicate"},
			{"State bucket state", "RETAINED", "recovery gate"},
			{"Unknown artifact", "FAILED", "no identity"},
		}, Backends: []nukeBackend{{"state", "us-east-2"}},
	}}
	calls := 0
	n.awsForRegion = func(region string) awsenv.API {
		if region != "us-east-2" {
			t.Fatal(region)
		}
		return nukeFakeAWS{func(result any, args ...string) (bool, error) {
			calls++
			switch strings.Join(args[:2], " ") {
			case "dynamodb describe-backup":
				return false, &awsenv.Error{Code: "BackupNotFoundException"}
			case "iam get-role":
				return true, nil
			case "s3api head-bucket":
				return false, &awsenv.Error{Code: "AccessDenied"}
			default:
				t.Fatalf("unexpected or mutating operation: %v", args)
				return false, nil
			}
		}}
	}
	if n.checkLastNuke() == nil {
		t.Fatal("remaining/unverified artifacts must fail the check")
	}
	if calls != 3 {
		t.Fatalf("calls=%d; duplicate should be checked once", calls)
	}
	want := []string{"ABSENT", "REMAINS", "UNVERIFIED", "UNVERIFIED"}
	for i, r := range n.receipt.CheckResults {
		if r.Status != want[i] {
			t.Fatal(n.receipt.CheckResults)
		}
	}
	if n.receipt.Results[0].Status != "FAILED" || n.receipt.Results[1].Status != "ABSENT" {
		t.Fatal("original history overwritten")
	}
	data, err := os.ReadFile(n.path)
	var saved nukeReceipt
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.CheckedAt == "" || len(saved.CheckResults) != 4 {
		t.Fatalf("check not persisted: %s, %v", data, err)
	}
}

func TestPostNukeCheckUsesSavedInventoryRegion(t *testing.T) {
	p := teardownProbe{Name: "certificate-urn", Kind: "certificate", ID: "arn:aws:acm:us-east-1:123456789012:certificate/id"}
	target := nukeTarget{Project: "attestra-auth-email", Stack: "dev", Backend: "s3://state", Region: "us-east-2", Regions: map[string]string{p.Name: "us-east-1"}, Evidence: teardownProgress{InventoryCaptured: true, Inventory: []teardownProbe{p}}}
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json"), receipt: nukeReceipt{Targets: []nukeTarget{target}, Results: []nukeResult{{nukeProbeLabel(&target, p), "ABSENT", "old"}}}}
	n.awsForRegion = func(region string) awsenv.API {
		if region != "us-east-1" {
			t.Fatal(region)
		}
		return nukeFakeAWS{func(any, ...string) (bool, error) { return false, &awsenv.Error{Code: "ResourceNotFoundException"} }}
	}
	if err := n.checkLastNuke(); err != nil {
		t.Fatal(err)
	}
	if n.receipt.CheckResults[0].Status != "ABSENT" {
		t.Fatal(n.receipt.CheckResults)
	}
}

func TestPostNukeCheckRejectsMissingHistoryAndPreservesReadErrors(t *testing.T) {
	n := &nukeWorkflow{path: filepath.Join(t.TempDir(), "report.json")}
	if n.checkLastNuke() == nil {
		t.Fatal("empty history accepted")
	}
	n.receipt.Account = "123456789012"
	n.receipt.Results = []nukeResult{{"Backup arn:aws:dynamodb:us-east-2:123456789012:table/data/backup/123", "FAILED", "old"}}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(any, ...string) (bool, error) { return false, errors.New("connection lost") }}
	}
	if n.checkLastNuke() == nil || n.receipt.CheckResults[0].Status != "UNVERIFIED" {
		t.Fatal(n.receipt.CheckResults)
	}
}

func TestPostNukeCheckDNSAndGitHubUseOnlyGET(t *testing.T) {
	zone := strings.Repeat("a", 32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("mutating request: %s", r.Method)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/environments":
			fmt.Fprint(w, `{"environments":[]}`)
		case "/zones":
			fmt.Fprintf(w, `{"success":true,"result":[{"id":%q,"name":"example.com","status":"active"}],"result_info":{"total_pages":1}}`, zone)
		case "/zones/" + zone + "/dns_records":
			fmt.Fprint(w, `{"success":true,"result":[],"result_info":{"total_pages":1}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	n := &nukeWorkflow{g: github.NewClient("test"), cf: dns.NewClient("test"), receipt: nukeReceipt{Repository: "o/r"}}
	n.g.BaseURL, n.cf.BaseURL = s.URL, s.URL
	if err := n.checkNukeArtifact("GitHub environment dev"); err != nil {
		t.Fatal(err)
	}
	if err := n.checkNukeDNS(dns.Record{Type: "CNAME", Name: "dev.example.com", Content: "old.example.net"}); err != nil {
		t.Fatal(err)
	}
	n.cf = nil
	if n.checkNukeDNS(dns.Record{Name: "dev.example.com"}) == nil {
		t.Fatal("missing DNS access accepted")
	}
}

func TestPostNukeCheckKMSPendingDeletionIsNotAbsent(t *testing.T) {
	n := &nukeWorkflow{receipt: nukeReceipt{Keys: []nukeKey{{"key", "us-east-2"}}}}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(result any, args ...string) (bool, error) {
			if strings.Join(args[:2], " ") != "kms describe-key" {
				t.Fatal(args)
			}
			return true, json.Unmarshal([]byte(`{"KeyMetadata":{"Arn":"arn:aws:kms:us-east-2:123456789012:key/key","KeyState":"PendingDeletion","DeletionDate":1794000000}}`), result)
		}}
	}
	err := n.checkNukeArtifact("KMS key")
	if !errors.Is(err, errTeardownResourceRemains) || !strings.Contains(err.Error(), "PendingDeletion") {
		t.Fatal(err)
	}
}

func TestPostNukeCheckStackDoesNotDestroyOrRequireMissingBackend(t *testing.T) {
	n := &nukeWorkflow{}
	target := nukeTarget{Backend: "s3://state", Region: "us-east-2", Project: "attestra-auth-email", Stack: "dev"}
	n.awsForRegion = func(string) awsenv.API { return nukeFakeAWS{func(any, ...string) (bool, error) { return true, nil }} }
	n.runnerForTarget = func(nukeTarget) commandRunner {
		return nukeRunnerFunc(func(_ string, capture bool, command string, args ...string) ([]byte, error) {
			if command != "pulumi" || strings.Join(args, " ") != "stack ls --json --non-interactive" || !capture {
				t.Fatal(command, args)
			}
			return []byte(`[]`), nil
		})
	}
	if err := n.checkNukeStack(target); err != nil {
		t.Fatal(err)
	}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(any, ...string) (bool, error) { return false, &awsenv.Error{Code: "NoSuchBucket"} }}
	}
	n.runnerForTarget = func(nukeTarget) commandRunner { t.Fatal("Pulumi used for absent bucket"); return nil }
	if err := n.checkNukeStack(target); err != nil {
		t.Fatal(err)
	}
}

func TestPostNukeCheckListsBackupsAndLogsWithoutCleanup(t *testing.T) {
	n := &nukeWorkflow{}
	n.awsForRegion = func(string) awsenv.API {
		return nukeFakeAWS{func(result any, args ...string) (bool, error) {
			switch strings.Join(args[:2], " ") {
			case "dynamodb list-backups":
				return true, json.Unmarshal([]byte(`{"BackupSummaries":[]}`), result)
			case "logs describe-log-groups":
				return true, json.Unmarshal([]byte(`{"logGroups":[{"logGroupName":"/aws/lambda/fn-extra"}]}`), result)
			default:
				t.Fatal(args)
				return false, nil
			}
		}}
	}
	if err := n.checkNukeLog("us-east-2", "/aws/lambda/fn"); err != nil {
		t.Fatal(err)
	}
	if err := n.checkNukeBackups("us-east-2", "data"); err != nil {
		t.Fatal(err)
	}
}

func TestPostNukeCheckRejectsInaccessibleGitHubAndIncompleteDNS(t *testing.T) {
	for _, body := range []string{"notfound", `{"environments":null}`, `{"environments":[{}]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == "notfound" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		}))
		n := &nukeWorkflow{g: github.NewClient("test"), receipt: nukeReceipt{Repository: "o/r"}}
		n.g.BaseURL = s.URL
		if err := n.checkNukeArtifact("GitHub environment dev"); err == nil || errors.Is(err, errTeardownResourceRemains) {
			t.Fatalf("inaccessible/malformed listing accepted: %s, %v", body, err)
		}
		s.Close()
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			fmt.Fprintf(w, `{"success":true,"result":[{"id":%q,"name":"example.com","status":"active"}]}`, strings.Repeat("a", 32))
		} else {
			fmt.Fprint(w, `{"success":true,"result":null}`)
		}
	}))
	defer s.Close()
	n := &nukeWorkflow{cf: dns.NewClient("test")}
	n.cf.BaseURL = s.URL
	if n.checkNukeDNS(dns.Record{Type: "CNAME", Name: "dev.example.com", Content: "old.example.net"}) == nil {
		t.Fatal("null DNS listing accepted as absence")
	}
}

func TestPostNukeCheckChecksEveryRegionForLegacyLabels(t *testing.T) {
	for _, kind := range []string{"lambda", "table", "kms"} {
		t.Run(kind, func(t *testing.T) {
			n := &nukeWorkflow{}
			for _, region := range []string{"us-east-1", "us-east-2"} {
				n.receipt.Targets = append(n.receipt.Targets, nukeTarget{Region: region, Evidence: teardownProgress{Inventory: []teardownProbe{{Kind: kind, ID: "same-name"}}}})
				n.receipt.Keys = append(n.receipt.Keys, nukeKey{"alias/same", region})
			}
			calls := 0
			n.awsForRegion = func(region string) awsenv.API {
				return nukeFakeAWS{func(result any, args ...string) (bool, error) {
					calls++
					if kind == "kms" {
						if region == "us-east-1" {
							return false, &awsenv.Error{Code: "NotFoundException"}
						}
						return true, json.Unmarshal([]byte(`{"KeyMetadata":{"Arn":"arn:aws:kms:us-east-2:123456789012:key/key","KeyState":"Enabled"}}`), result)
					}
					body := `{"logGroups":[],"BackupSummaries":[]}`
					if region == "us-east-2" {
						body = `{"logGroups":[{"logGroupName":"/aws/lambda/same-name"}],"BackupSummaries":[{"BackupArn":"backup"}]}`
					}
					return true, json.Unmarshal([]byte(body), result)
				}}
			}
			label := "Log group /aws/lambda/same-name"
			if kind == "table" {
				label = "Backups for same-name"
			}
			if kind == "kms" {
				label = "KMS alias/same"
			}
			if err := n.checkNukeArtifact(label); !errors.Is(err, errTeardownResourceRemains) {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("checked %d regions", calls)
			}
		})
	}
}
