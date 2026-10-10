package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type indexMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *indexMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}
func (m *indexMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	out := args.Inputs.Copy()
	out["arn"] = resource.NewStringProperty("arn:example:" + args.Name)
	switch args.TypeToken {
	case "aws:acm/certificate:Certificate":
		out["domainValidationOptions"] = resource.NewArrayProperty([]resource.PropertyValue{resource.NewObjectProperty(resource.NewPropertyMapFromMap(map[string]any{"resourceRecordName": "_token.index.example.com", "resourceRecordType": "CNAME", "resourceRecordValue": "_token.acm-validations.aws"}))})
	case "aws:apigatewayv2/api:Api":
		out["apiEndpoint"] = resource.NewStringProperty("https://abc.execute-api.us-east-2.amazonaws.com")
		out["executionArn"] = resource.NewStringProperty("arn:aws:execute-api:us-east-2:123456789012:abc")
	case "aws:apigatewayv2/domainName:DomainName":
		cfg := out["domainNameConfiguration"].ObjectValue().Copy()
		cfg["targetDomainName"] = resource.NewStringProperty("target.execute-api.us-east-2.amazonaws.com")
		out["domainNameConfiguration"] = resource.NewObjectProperty(cfg)
	}
	return args.Name + "-id", out, nil
}
func TestIndexInfrastructure(t *testing.T) {
	for _, zone := range []string{"", "Z123EXAMPLE"} {
		t.Run("zone="+zone, func(t *testing.T) {
			root := t.TempDir()
			os.MkdirAll(filepath.Join(root, "infra-index"), 0755)
			os.MkdirAll(filepath.Join(root, "dist"), 0755)
			f, _ := os.Create(filepath.Join(root, "dist/index.zip"))
			z := zip.NewWriter(f)
			entry, _ := z.Create("bootstrap")
			entry.Write([]byte("test"))
			z.Close()
			f.Close()
			t.Chdir(filepath.Join(root, "infra-index"))
			t.Setenv("PULUMI_CONFIG", `{"attestra-index:domain":"index.example.com","attestra-index:sourceHash":"test","attestra-index:route53ZoneId":"`+zone+`"}`)
			m := &indexMocks{resources: map[string]pulumi.MockResourceArgs{}}
			if e := pulumi.RunErr(deployIndex, pulumi.WithMocks("attestra-index", "shared", m)); e != nil {
				t.Fatal(e)
			}
			if m.resources["index-write"].Inputs["authorizationType"].StringValue() != "AWS_IAM" {
				t.Fatal("unauthenticated registry writes")
			}
			if m.resources["index-read"].Inputs["authorizationType"].StringValue() != "NONE" {
				t.Fatal("discovery is not public")
			}
			if m.resources["index-handler"].Inputs["reservedConcurrentExecutions"].NumberValue() != -1 {
				t.Fatal("registry reserves scarce Lambda capacity")
			}
			_, dns := m.resources["index-hostname"]
			if dns != (zone != "") {
				t.Fatal("wrong DNS provider path")
			}
			if !m.resources["environment-registry"].RegisterRPC.GetProtect() {
				t.Fatal("shared registry lacks deletion protection")
			}
		})
	}
}
