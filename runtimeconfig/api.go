package runtimeconfig

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
)

type Reader interface {
	Get(context.Context, string) (Config, error)
}
type PublicHandler struct {
	Store       Reader
	Environment string
}

func (h PublicHandler) Handle(ctx context.Context, r events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	headers := map[string]string{"content-type": "application/json", "cache-control": "no-store", "x-content-type-options": "nosniff"}
	if r.RawPath != "/v1/app-config" || r.RequestContext.HTTP.Method != "GET" {
		return events.APIGatewayV2HTTPResponse{StatusCode: 404, Headers: headers, Body: `{"error":"not_found"}`}, nil
	}
	c, e := h.Store.Get(ctx, h.Environment)
	if e != nil || c.Environment != h.Environment || c.Validate() != nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: 503, Headers: headers, Body: `{"error":"config_unavailable"}`}, nil
	}
	b, e := json.Marshal(c.Normalized())
	if e != nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: 503, Headers: headers, Body: `{"error":"config_unavailable"}`}, nil
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: 200, Headers: headers, Body: string(b)}, nil
}
