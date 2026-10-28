package main

import (
	"encoding/json"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
)

// An omitted deployment flag uses the new default; explicit false preserves the
// operator's existing disabled capture choice. Published overrides remain separate.
func deploymentRuntimeDefaults(environment, captureEnabled string) runtimeconfig.Config {
	defaults := runtimeconfig.Default(environment)
	defaults.Features.IDCapture = captureEnabled != "false"
	if !defaults.Features.IDCapture {
		defaults.Onboarding.IDCapture = runtimeconfig.Disabled
	}
	return defaults
}

func enrollmentPolicy(route, environment, runtimeARN, factsARN, captureARN, poolARN string) string {
	statement := func(action any, resource string, condition any) map[string]any {
		s := map[string]any{"Effect": "Allow", "Action": action, "Resource": resource}
		if condition != nil {
			s["Condition"] = condition
		}
		return s
	}
	keys := func(operator string, values ...string) any {
		return map[string]any{operator: map[string]any{"dynamodb:LeadingKeys": values}}
	}
	statements := []map[string]any{
		statement("dynamodb:GetItem", runtimeARN, keys("ForAllValues:StringEquals", "ENV#"+environment)),
		statement([]string{"dynamodb:GetItem", "dynamodb:UpdateItem"}, factsARN, keys("ForAllValues:StringLike", "enrollment#*")),
	}
	if route == "status" {
		statements = append(statements, statement("dynamodb:GetItem", captureARN, keys("ForAllValues:StringLike", "ACCOUNT#*", "CAP#*")))
	} else {
		statements = append(statements, statement("cognito-idp:AdminSetUserPassword", poolARN, nil))
	}
	data, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": statements})
	return string(data)
}
