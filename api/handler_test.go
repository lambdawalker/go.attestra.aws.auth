package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
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
