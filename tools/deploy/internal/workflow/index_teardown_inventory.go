package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
	"strings"
)

// Check raw DynamoDB rows, not the public list: retired entries can still hold
// a teardown lock, and unfinished deployments may not have published an entry.
func indexRegistryEmpty(data []byte) error {
	var page struct{ Items []map[string]json.RawMessage }
	if json.Unmarshal(data, &page) != nil || page.Items == nil {
		return errors.New("invalid registry scan; cannot establish that no environments remain")
	}
	for _, row := range page.Items {
		var id struct{ S string }
		var deleted struct{ BOOL bool }
		var lock struct{ S string }
		if json.Unmarshal(row["id"], &id) != nil || id.S == "" || json.Unmarshal(row["deleted"], &deleted) != nil || !deleted.BOOL {
			return errors.New("registry contains an environment or incomplete deployment; finish environment teardown first")
		}
		if raw, ok := row["lock"]; ok {
			if json.Unmarshal(raw, &lock) != nil || lock.S != "" {
				return errors.New("registry contains a deployment/teardown lock; resolve it before removing the index")
			}
		}
		if _, ok := row["entry"]; ok {
			return errors.New("registry still contains a published environment; finish environment teardown first")
		}
	}
	return nil
}

func checkIndexRegistry(a awsenv.API, table string, allowMissing bool) error {
	if table == "" {
		return nil
	} // Certificate-only partial setup.
	var start json.RawMessage
	for {
		args := []string{"dynamodb", "scan", "--table-name", table, "--consistent-read", "--no-paginate"}
		if len(start) > 0 {
			args = append(args, "--exclusive-start-key", string(start))
		}
		var page json.RawMessage
		_, err := a.Call(&page, args...)
		if err != nil {
			var ae *awsenv.Error
			if allowMissing && errors.As(err, &ae) && ae.Code == "ResourceNotFoundException" {
				return nil
			}
			return err
		}
		if err = indexRegistryEmpty(page); err != nil {
			return err
		}
		var next struct{ LastEvaluatedKey json.RawMessage }
		if err = json.Unmarshal(page, &next); err != nil {
			return err
		}
		if len(next.LastEvaluatedKey) == 0 || string(next.LastEvaluatedKey) == "{}" || string(next.LastEvaluatedKey) == "null" {
			return nil
		}
		if string(start) == string(next.LastEvaluatedKey) {
			return errors.New("registry scan did not advance")
		}
		start = next.LastEvaluatedKey
	}
}

func captureIndexTeardown(data []byte, p *teardownProgress) error {
	var state struct {
		Deployment *struct {
			Resources []struct {
				URN, Type, ID                     string
				Protect, External, RetainOnDelete bool
				Outputs                           map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment == nil || state.Deployment.Resources == nil {
		return errors.New("invalid shared index export")
	}
	for _, r := range state.Deployment.Resources {
		if !strings.HasPrefix(r.URN, "urn:pulumi:shared::attestra-index::") {
			return errors.New("resource does not belong to attestra-index/shared")
		}
		if r.External || r.RetainOnDelete {
			return errors.New("shared index has external/retained resources; review ownership before teardown")
		}
		if r.Protect && !(r.Type == "aws:dynamodb/table:Table" && strings.HasSuffix(r.URN, "::environment-registry")) {
			return errors.New("unexpected protected resource in index stack; manual review required")
		}
		if r.Type == "aws:dynamodb/table:Table" {
			if !strings.HasSuffix(r.URN, "::environment-registry") {
				return errors.New("unexpected index table")
			}
			if p.Bucket != "" && p.Bucket != r.ID {
				return errors.New("registry table changed since teardown started")
			}
			p.Bucket, p.Values["tableURN"] = r.ID, r.URN
		}
		if r.Type == "aws:acm/certificate:Certificate" {
			var opts []struct{ ResourceRecordName, ResourceRecordType, ResourceRecordValue string }
			if raw := r.Outputs["domainValidationOptions"]; len(raw) > 0 {
				if err := json.Unmarshal(raw, &opts); err != nil {
					return err
				}
			}
			for _, option := range opts {
				if option.ResourceRecordName != "" && option.ResourceRecordValue != "" {
					p.Desired = append(p.Desired, dns.Record{Type: option.ResourceRecordType, Name: option.ResourceRecordName, Content: option.ResourceRecordValue})
				}
			}
		}
	}
	if err := captureTeardownInventory(data, p); err != nil {
		return err
	}
	for _, probe := range p.Inventory {
		if probe.Kind == "unsupported" || probe.ID == "" {
			return fmt.Errorf("cannot verify removal of %s", probe.Name)
		}
	}
	p.InventoryCaptured = true
	return nil
}
