package main

import (
	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"strings"
	"sync"
	"testing"
)

type associationMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *associationMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return a.Args, nil
}
func (m *associationMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[a.Name] = a
	out := a.Inputs.Copy()
	out["arn"] = resource.NewStringProperty("arn:example:" + a.Name)
	out["bucketRegionalDomainName"] = resource.NewStringProperty("bucket.s3.us-east-2.amazonaws.com")
	out["domainName"] = resource.NewStringProperty("distribution.cloudfront.net")
	out["hostedZoneId"] = resource.NewStringProperty("ZONE")
	out["fqdn"] = resource.NewStringProperty("_token.dev.example.com")
	if a.TypeToken == "aws:acm/certificate:Certificate" {
		out["domainValidationOptions"] = resource.NewArrayProperty([]resource.PropertyValue{resource.NewObjectProperty(resource.NewPropertyMapFromMap(map[string]any{"resourceRecordName": "_token.dev.example.com", "resourceRecordType": "CNAME", "resourceRecordValue": "_validation.acm-validations.aws"}))})
	}
	return a.Name + "-id", out, nil
}
func TestAssociationInfrastructure(t *testing.T) {
	for _, zone := range []string{"", "Z123"} {
		t.Run(zone, func(t *testing.T) {
			m := &associationMocks{resources: map[string]pulumi.MockResourceArgs{}}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				return deployAssetLinks(ctx, "https://dev.example.com", zone, []assetlinks.Application{{PackageName: "com.example.dev", Fingerprints: []string{strings.Repeat("AB:", 31) + "AB"}}})
			}, pulumi.WithMocks("attestra-auth-email", "dev", m))
			if err != nil {
				t.Fatal(err)
			}
			if m.resources["app-edge"].Inputs["region"].StringValue() != "us-east-1" {
				t.Fatal("wrong certificate region")
			}
			if !strings.Contains(m.resources["app-certificate"].Provider, "app-edge") {
				t.Fatal("certificate does not use edge provider")
			}
			if !m.resources["app-associations-private"].Inputs["blockPublicPolicy"].BoolValue() {
				t.Fatal("public bucket")
			}
			if m.resources["android-assetlinks"].Inputs["contentType"].StringValue() != "application/json" {
				t.Fatal("wrong content type")
			}
			behavior := m.resources["app-associations-cdn"].Inputs["defaultCacheBehavior"].ObjectValue()
			if behavior["cachePolicyId"].StringValue() != "4135ea2d-6df8-44a3-9df3-4b5a84be39ad" {
				t.Fatal("association responses cached")
			}
			if !strings.Contains(m.resources["app-associations-policy"].Inputs["policy"].StringValue(), "AWS:SourceArn") {
				t.Fatal("origin not scoped to distribution")
			}
			_, hasDNS := m.resources["app-associations-dns"]
			if hasDNS != (zone != "") {
				t.Fatal("wrong DNS provider behavior")
			}
		})
	}
}
