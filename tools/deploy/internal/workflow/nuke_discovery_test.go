package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
)

func TestNukeDiscoveryKeepsLocalTargetsWhenRemoteArtifactsAreAbsent(t *testing.T) {
	for _, tc := range []struct{ name, body, project string }{
		{"bootstrap.dev.local.json", `{"Repository":"o/r","Stack":"dev","Backend":"s3://gone","Region":"us-east-2"}`, "attestra-auth-email"},
		{"deploy.qa.local.json", `{"Repository":"o/r","Stack":"qa","Backend":"s3://gone","Region":"us-east-2"}`, "attestra-auth-email"},
		{"teardown-index.123456789012.us-east-2.local.json", `{"Environment":"shared","Values":{"account":"123456789012","region":"us-east-2","backend":"s3://gone"}}`, "attestra-index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_ = os.MkdirAll(filepath.Join(root, ".attestra"), 0700)
			_ = os.WriteFile(filepath.Join(root, ".attestra", tc.name), []byte(tc.body), 0600)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"environments":[]}`)) }))
			defer s.Close()
			g := github.NewClient("test")
			g.BaseURL = s.URL
			n := &nukeWorkflow{root: root, path: filepath.Join(root, ".attestra", "report.json"), g: g, receipt: nukeReceipt{Repository: "o/r", Account: "123456789012"}}
			n.awsForRegion = func(string) awsenv.API {
				return nukeFakeAWS{func(result any, args ...string) (bool, error) {
					if args[1] == "list-buckets" {
						_ = json.Unmarshal([]byte(`{"Buckets":[]}`), result)
						return true, nil
					}
					return false, &awsenv.Error{Code: "NoSuchBucket"}
				}}
			}
			n.discover()
			if len(n.receipt.Targets) != 1 || n.receipt.Targets[0].Project != tc.project {
				t.Fatalf("lost environment discovery evidence: %+v", n.receipt)
			}
		})
	}
}

func TestNukeAddTargetPreservesSurvivingReceiptInventory(t *testing.T) {
	n := &nukeWorkflow{}
	base := nukeTarget{Backend: "s3://state", Region: "us-east-2", Project: "attestra-auth-email", Stack: "dev"}
	n.addTarget(base)
	base.Evidence = teardownProgress{InventoryCaptured: true, Inventory: []teardownProbe{{Name: "fn", Kind: "lambda", ID: "fn"}}}
	n.addTarget(base)
	if len(n.receipt.Targets) != 1 || !n.receipt.Targets[0].Evidence.InventoryCaptured || len(n.receipt.Targets[0].Evidence.Inventory) != 1 {
		t.Fatal(n.receipt.Targets)
	}
}
