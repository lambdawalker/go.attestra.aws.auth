package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestRuntimeDefaultsSurvivePulumiSerialization(t *testing.T) {
	defaults := runtimeconfig.Default("dev")
	defaults.Features.IDCapture = true
	defaults.Capture = runtimeconfig.Capture{DocumentType: "driver_license", Jurisdiction: "MX", Purpose: "Identity verification"}
	m := &associationMocks{resources: map[string]pulumi.MockResourceArgs{}}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		// Resource inputs and stack exports use Pulumi's property serializer.
		var state pulumi.CustomResourceState
		return ctx.RegisterResource("test:index:Defaults", "defaults", pulumi.Map{"runtimeDefaults": pulumi.Any(defaults)}, &state)
	}, pulumi.WithMocks("attestra-auth-email", "dev", m))
	if err != nil {
		t.Fatal(err)
	}
	value := m.resources["defaults"].Inputs["runtimeDefaults"].ObjectValue().Mappable()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got runtimeconfig.Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("invalid serialized defaults %s: %v", data, err)
	}
	if !reflect.DeepEqual(defaults, got) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, defaults)
	}
}
