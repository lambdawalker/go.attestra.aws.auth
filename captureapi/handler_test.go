package captureapi

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"strings"
	"testing"
)

func TestRejectsUnverifiedAndIDTokens(t *testing.T) {
	h := Handler{Service: capture.Service{}}
	for _, tokenUse := range []string{"", "id"} {
		req := events.APIGatewayV2HTTPRequest{RawPath: "/onboarding/id/document-policy"}
		req.RequestContext.HTTP.Method = "GET"
		if tokenUse != "" {
			req.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{Claims: map[string]string{"sub": "owner", "token_use": tokenUse}}}
		}
		r, e := h.Handle(context.Background(), req)
		if e != nil || r.StatusCode != 401 {
			t.Fatal("unverified token accepted", r, e)
		}
	}
}
func TestStrictBodies(t *testing.T) {
	for _, body := range []string{`{"owner":"another-account"}`, `{} {}`, strings.Repeat("x", 12001)} {
		if e := decode(events.APIGatewayV2HTTPRequest{Body: body}, &struct{}{}); e != capture.ErrInvalid {
			t.Fatal("unsafe body accepted")
		}
	}
}

func TestResetTokenGateRejectsOldAccessToken(t *testing.T) {
	h := Handler{CheckToken: func(_ context.Context, iat int64) (bool, error) { return iat > 100, nil }}
	req := events.APIGatewayV2HTTPRequest{RawPath: "/onboarding/id/status"}
	req.RequestContext.HTTP.Method = "GET"
	req.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{Claims: map[string]string{"sub": "old-user", "token_use": "access", "iat": "100"}}}
	out, e := h.Handle(context.Background(), req)
	if e != nil || out.StatusCode != 401 {
		t.Fatal("pre-reset token accepted", e)
	}
}
