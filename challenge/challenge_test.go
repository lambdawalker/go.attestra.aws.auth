package challenge

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDefineChallengePreservesCognitoEvent(t *testing.T) {
	input := json.RawMessage(`{
		"version":"1","triggerSource":"DefineAuthChallenge_Authentication",
		"region":"us-east-2","userPoolId":"us-east-2_example","userName":"example",
		"callerContext":{"awsSdkVersion":"aws-sdk-go","clientId":"client-id"},
		"request":{"userAttributes":{"sub":"subject"},"session":[],"clientMetadata":{"future":"value"}},
		"response":{},"futureField":{"keep":true}
	}`)
	result, err := (Handler{}).Handle(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "region", "userPoolId", "userName", "callerContext", "request", "futureField"} {
		if got[key] == nil {
			t.Errorf("Cognito event lost %s: %s", key, encoded)
		}
	}
	var response struct {
		ChallengeName      string `json:"challengeName"`
		IssueTokens        bool   `json:"issueTokens"`
		FailAuthentication bool `json:"failAuthentication"`
	}
	if err := json.Unmarshal(got["response"], &response); err != nil {
		t.Fatal(err)
	}
	if response.ChallengeName != "CUSTOM_CHALLENGE" {
		t.Fatalf("unexpected challenge: %s", got["response"])
	}
	if response.IssueTokens || response.FailAuthentication {
		t.Fatalf("initial challenge must continue: %s", got["response"])
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got["response"], &fields); err != nil {
		t.Fatal(err)
	}
	if fields["issueTokens"] == nil || fields["failAuthentication"] == nil {
		t.Fatalf("missing Cognito decision fields: %s", got["response"])
	}
}
