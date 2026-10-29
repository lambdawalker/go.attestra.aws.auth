package main

import (
	"encoding/json"
	"testing"
)

func TestCaptureWorkerCanCommitReadyAccountUpdate(t *testing.T) {
	const tableARN = "arn:aws:dynamodb:us-east-2:123456789012:table/captures"
	var policy struct {
		Statement []struct {
			Effect    string
			Action    []string
			Resource  string
			Condition map[string]map[string][]string
		}
	}
	if err := json.Unmarshal([]byte(`{"Statement":[`+captureWorkerDatabaseStatements(tableARN)+`]}`), &policy); err != nil {
		t.Fatal(err)
	}
	allowed := false
	for _, statement := range policy.Statement {
		for _, action := range statement.Action {
			if action != "dynamodb:UpdateItem" {
				continue
			}
			if statement.Effect != "Allow" || statement.Resource != tableARN {
				t.Fatal("account update must target capture table")
			}
			keys := statement.Condition["ForAllValues:StringLike"]["dynamodb:LeadingKeys"]
			if len(keys) != 1 || keys[0] != "ACCOUNT#*" {
				t.Fatal("account update must be scoped to account records")
			}
			allowed = true
		}
	}
	if !allowed {
		t.Fatal("ready transaction uses UpdateItem for the durable account completion flag, but the worker role does not allow it")
	}
}
