package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

func TestEndpointHandlersRejectOtherRoutes(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		handle func(context.Context, events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)
	}{
		{"signup", "/signup", (Handler{}).Signup},
		{"resend", "/resend", (Handler{}).Resend},
		{"confirm", "/confirm", (Handler{}).Confirm},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrongPath := events.APIGatewayV2HTTPRequest{RawPath: "/other"}
			wrongPath.RequestContext.HTTP.Method = "POST"
			wrongMethod := events.APIGatewayV2HTTPRequest{RawPath: tc.path}
			wrongMethod.RequestContext.HTTP.Method = "GET"
			for _, req := range []events.APIGatewayV2HTTPRequest{wrongPath, wrongMethod} {
				response, err := tc.handle(context.Background(), req)
				if err != nil || response.StatusCode != 404 {
					t.Fatalf("wrong route or method: status=%d err=%v", response.StatusCode, err)
				}
			}
		})
	}
}
func TestOperationalFailureReportsSafeStage(t *testing.T) {
	resp := failure(&email.OperationalError{Stage: "ses_send", Cause: errors.New("secret email code: 123456")})
	if resp.StatusCode != 503 || strings.Contains(resp.Body, "123456") {
		t.Fatalf("unsafe operational response: %+v", resp)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil || body["stage"] != "ses_send" {
		t.Fatalf("missing safe failure stage: %v / %v", body, err)
	}
}
func TestFailureIncludesGatewayTraceWithoutSecrets(t *testing.T) {
	req := events.APIGatewayV2HTTPRequest{}
	req.RequestContext.RequestID = "gateway-123"
	resp := done("signup", req, failure(&email.OperationalError{Stage: "ses_send", Code: "MessageRejected", Cause: errors.New("address@example.com token-secret")}))
	if resp.Headers["x-request-id"] != "gateway-123" || !strings.Contains(resp.Body, `"trace_id":"gateway-123"`) || strings.Contains(resp.Body, "token-secret") {
		t.Fatalf("unsafe or missing trace: %+v", resp)
	}
}
