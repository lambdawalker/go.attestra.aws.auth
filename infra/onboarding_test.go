package main

import (
	"encoding/json"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"reflect"
	"testing"
)

func TestOnboardingDeploymentDefaults(t *testing.T) {
	for _, tc := range []struct {
		setting string
		enabled bool
	}{{"", true}, {"true", true}, {"false", false}} {
		got := deploymentRuntimeDefaults("dev", tc.setting)
		if got.Features.IDCapture != tc.enabled {
			t.Fatalf("setting %q: %+v", tc.setting, got)
		}
		want := runtimeconfig.Required
		if !tc.enabled {
			want = runtimeconfig.Disabled
		}
		if got.Onboarding.IDCapture != want || got.Onboarding.Passkey != runtimeconfig.Required || got.Onboarding.Password != runtimeconfig.Disabled {
			t.Fatalf("setting %q: %+v", tc.setting, got.Onboarding)
		}
		if err := got.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEnrollmentPolicyScopes(t *testing.T) {
	for _, route := range []string{"status", "password/set"} {
		var doc struct {
			Statement []struct {
				Action    interface{}
				Resource  string
				Condition map[string]map[string][]string
			}
		}
		if err := json.Unmarshal([]byte(enrollmentPolicy(route, "dev", "runtime", "facts", "captures", "pool")), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Statement) != 3 {
			t.Fatalf("unexpected grants: %+v", doc)
		}
		runtime := doc.Statement[0]
		if runtime.Resource != "runtime" || runtime.Action != "dynamodb:GetItem" || runtime.Condition["ForAllValues:StringEquals"]["dynamodb:LeadingKeys"][0] != "ENV#dev" {
			t.Fatalf("runtime scope: %+v", runtime)
		}
		facts := doc.Statement[1]
		if facts.Resource != "facts" || !reflect.DeepEqual(facts.Action, []interface{}{"dynamodb:GetItem", "dynamodb:UpdateItem"}) || facts.Condition["ForAllValues:StringLike"]["dynamodb:LeadingKeys"][0] != "enrollment#*" {
			t.Fatalf("facts scope: %+v", facts)
		}
		last := doc.Statement[2]
		if route == "password/set" {
			if last.Resource != "pool" || last.Action != "cognito-idp:AdminSetUserPassword" {
				t.Fatalf("password scope: %+v", last)
			}
		} else {
			if last.Resource != "captures" || last.Action != "dynamodb:GetItem" || !reflect.DeepEqual(last.Condition["ForAllValues:StringLike"]["dynamodb:LeadingKeys"], []string{"ACCOUNT#*", "CAP#*"}) {
				t.Fatalf("capture scope: %+v", last)
			}
		}
	}
}
