package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func nukeMissing(err error, codes ...string) bool {
	var ae *awsenv.Error
	return errors.As(err, &ae) && slices.Contains(codes, ae.Code)
}

func (n *nukeWorkflow) addBackend(backend, region string) {
	o := options{Backend: backend, Region: region, Stack: "nuke"}
	if err := o.validate(); err != nil {
		n.failure("Backend "+backend, err)
		return
	}
	u, _ := url.Parse(backend)
	if u.Path != "" && u.Path != "/" {
		n.failure("Backend "+backend, errors.New("prefixed state backend requires manual review; whole-bucket deletion could remove unrelated state"))
		return
	}
	b := nukeBackend{u.Host, region}
	if !slices.Contains(n.receipt.Backends, b) {
		n.receipt.Backends = append(n.receipt.Backends, b)
	}
}

func (n *nukeWorkflow) addTarget(t nukeTarget) {
	for i, old := range n.receipt.Targets {
		if old.Backend == t.Backend && old.Project == t.Project && old.Stack == t.Stack {
			if old.Evidence.Values == nil {
				n.receipt.Targets[i].Evidence.Values = map[string]string{}
			}
			for k, v := range t.Evidence.Values {
				n.receipt.Targets[i].Evidence.Values[k] = v
			}
			p := &n.receipt.Targets[i].Evidence
			p.InventoryCaptured = p.InventoryCaptured || t.Evidence.InventoryCaptured
			for _, probe := range t.Evidence.Inventory {
				known := false
				for _, existing := range p.Inventory {
					if existing.Name == probe.Name && existing.ID == probe.ID {
						known = true
						break
					}
				}
				if !known {
					p.Inventory = append(p.Inventory, probe)
				}
			}
			for _, want := range t.Evidence.Desired {
				if !slices.Contains(p.Desired, want) {
					p.Desired = append(p.Desired, want)
				}
			}
			if p.Environment == "" {
				p.Environment = t.Evidence.Environment
			}
			if n.receipt.Targets[i].Regions == nil {
				n.receipt.Targets[i].Regions = map[string]string{}
			}
			for urn, region := range t.Regions {
				n.receipt.Targets[i].Regions[urn] = region
			}
			return
		}
	}
	n.receipt.Targets = append(n.receipt.Targets, t)
}

func (n *nukeWorkflow) discover() {
	// Existing receipts are also discovery evidence when GitHub or state is gone.
	paths, _ := filepath.Glob(filepath.Join(n.root, ".attestra", "*.local.json"))
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "teardown-index.") {
			b, err := os.ReadFile(path)
			var p teardownProgress
			if err != nil {
				n.failure(name, err)
				continue
			}
			if json.Unmarshal(b, &p) != nil {
				n.failure(name, errors.New("invalid shared teardown receipt"))
				continue
			}
			if p.Values["account"] != n.receipt.Account {
				continue
			}
			n.addBackend(p.Values["backend"], p.Values["region"])
			n.addTarget(nukeTarget{Backend: p.Values["backend"], Region: p.Values["region"], Project: "attestra-index", Stack: "shared", Evidence: p})
			continue
		}
		if !strings.HasPrefix(name, "bootstrap.") && !strings.HasPrefix(name, "deploy.") && !strings.HasPrefix(name, "teardown.") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			n.failure(name, err)
			continue
		}
		var record struct {
			Repository, Environment, Stack, Backend, Region string
			Values                                          map[string]string
		}
		if json.Unmarshal(b, &record) != nil {
			n.failure(name, errors.New("invalid local record"))
			continue
		}
		if record.Repository != n.receipt.Repository {
			continue
		}
		if strings.HasPrefix(name, "teardown.") {
			var p teardownProgress
			if json.Unmarshal(b, &p) != nil {
				continue
			}
			n.addBackend(p.Values["PULUMI_BACKEND_URL"], p.Values["AWS_REGION"])
			n.addTarget(nukeTarget{Backend: p.Values["PULUMI_BACKEND_URL"], Region: p.Values["AWS_REGION"], Project: "attestra-auth-email", Stack: p.Environment, Evidence: p})
		} else if record.Backend != "" {
			n.addBackend(record.Backend, record.Region)
			stack := record.Stack
			if stack == "" {
				stack = record.Environment
			}
			if stack != "" {
				n.addTarget(nukeTarget{Backend: record.Backend, Region: record.Region, Project: "attestra-auth-email", Stack: stack, Evidence: teardownProgress{Repository: n.receipt.Repository, Environment: stack, Values: map[string]string{}}})
			}
		}
	}
	names, err := n.g.ListEnvironments(n.receipt.Repository)
	if err != nil {
		n.failure("GitHub environment discovery", err)
	}
	for _, name := range names {
		snapshot, err := n.g.InspectEnvironment(n.receipt.Repository, name)
		if err != nil {
			n.failure("GitHub environment "+name, err)
			continue
		}
		v := snapshot.Variables
		if v["PULUMI_BACKEND_URL"] == "" && v["AWS_ROLE_ARN"] == "" {
			continue
		}
		if v["AWS_ACCOUNT_ID"] != n.receipt.Account {
			n.failure("GitHub environment "+name, errors.New("AWS account mismatch; environment excluded"))
			continue
		}
		if err := ui.ValidateEnvironment(name); err != nil {
			n.failure("GitHub environment "+name, err)
			continue
		}
		if !slices.Contains(n.receipt.Environments, name) {
			n.receipt.Environments = append(n.receipt.Environments, name)
		}
		n.addBackend(v["PULUMI_BACKEND_URL"], v["AWS_REGION"])
		stack := v["PULUMI_STACK"]
		if stack == "" {
			stack = name
		}
		n.addTarget(nukeTarget{Backend: v["PULUMI_BACKEND_URL"], Region: v["AWS_REGION"], Project: "attestra-auth-email", Stack: stack, Evidence: teardownProgress{Repository: n.receipt.Repository, Environment: name, Values: v}})
	}
	// This catches remote-only environments and the independently deployed index,
	// including environments whose GitHub configuration was already removed.
	var buckets struct{ Buckets *[]struct{ Name string } }
	if _, err := n.api("us-east-2").Call(&buckets, "s3api", "list-buckets"); err != nil {
		n.failure("State bucket discovery", err)
	} else if buckets.Buckets == nil {
		n.failure("State bucket discovery", errors.New("incomplete bucket listing"))
	} else {
		for _, b := range *buckets.Buckets {
			if !strings.HasPrefix(b.Name, "attestra-state-") && !strings.HasPrefix(b.Name, "attestra-index-state-"+n.receipt.Account+"-") {
				continue
			}
			var loc struct{ LocationConstraint *string }
			if _, err := n.api("us-east-2").Call(&loc, "s3api", "get-bucket-location", "--bucket", b.Name, "--expected-bucket-owner", n.receipt.Account); err != nil {
				n.failure("State bucket "+b.Name, err)
				continue
			}
			region := "us-east-1"
			if loc.LocationConstraint != nil {
				region = *loc.LocationConstraint
			}
			if region == "EU" {
				region = "eu-west-1"
			}
			n.addBackend("s3://"+b.Name, region)
		}
	}
	for _, b := range n.receipt.Backends {
		a := n.api(b.Region)
		_, err := a.Call(nil, "s3api", "head-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
		if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
			n.record(nukeResult{"State bucket " + b.Bucket, "ABSENT", "already absent"})
			continue
		}
		if err != nil {
			n.failure("State bucket "+b.Bucket, err)
			continue
		}
		for _, project := range []string{"attestra-auth-email", "attestra-index"} {
			t := nukeTarget{Backend: "s3://" + b.Bucket, Region: b.Region, Project: project}
			r := n.runner(t)
			data, err := r.Exec(n.dir(t), true, "pulumi", "stack", "ls", "--json", "--non-interactive")
			if err != nil {
				n.failure("Stack discovery "+t.Backend+"/"+project, err)
				continue
			}
			var rows []struct{ Name string }
			if json.Unmarshal(data, &rows) != nil || rows == nil {
				n.failure("Stack discovery "+t.Backend, errors.New("invalid stack listing"))
				continue
			}
			for _, row := range rows {
				parts := strings.Split(row.Name, "/")
				stack := parts[len(parts)-1]
				if err := ui.ValidateEnvironment(stack); err != nil {
					n.failure("Stack "+row.Name, err)
					continue
				}
				t.Stack = stack
				if project == "attestra-auth-email" {
					t.Evidence = teardownProgress{Repository: n.receipt.Repository, Environment: stack, Values: map[string]string{}}
				}
				n.addTarget(t)
			}
		}
	}
	// Stable ordering makes the reviewed scope and the report easy to compare.
	slices.SortFunc(n.receipt.Targets, func(a, b nukeTarget) int {
		return strings.Compare(a.Project+a.Backend+a.Stack, b.Project+b.Backend+b.Stack)
	})
}

func validateNukeState(data []byte, project, stack, account string) error {
	var state struct {
		Deployment *struct {
			Resources *[]struct {
				URN, Type, ID string
				Inputs        map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment == nil || state.Deployment.Resources == nil {
		return errors.New("invalid stack inventory")
	}
	for _, r := range *state.Deployment.Resources {
		if !strings.HasPrefix(r.URN, "urn:pulumi:"+stack+"::"+project+"::") {
			return errors.New("resource belongs to a different stack/project")
		}
		if strings.HasPrefix(r.ID, "arn:") {
			parts := strings.SplitN(r.ID, ":", 6)
			if len(parts) != 6 || parts[4] != "" && parts[4] != account {
				return errors.New("resource belongs to a different AWS account")
			}
		}
		if r.Type == "pulumi:providers:aws" {
			for _, key := range []string{"assumeRole", "assumeRoles", "assumeRoleWithWebIdentity", "endpoints", "profile", "accessKey", "secretKey", "token"} {
				if raw := r.Inputs[key]; len(raw) > 0 && string(raw) != "null" && string(raw) != `""` && string(raw) != "[]" {
					return fmt.Errorf("AWS provider override %s requires manual review", key)
				}
			}
		}
	}
	return nil
}
