package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"testing"
)

type readResult struct {
	config Config
	err    error
}

func (r readResult) Get(context.Context, string) (Config, error) { return r.config, r.err }
func TestPublicHandlerNarrowEnvironmentBoundAndFailsClosed(t *testing.T) {
	req := events.APIGatewayV2HTTPRequest{RawPath: "/v1/app-config", RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"}}}
	config := Default("dev")
	handler := PublicHandler{Environment: "dev", Store: readResult{config: config}}
	out, _ := handler.Handle(context.Background(), req)
	if out.StatusCode != 200 || out.Headers["cache-control"] != "no-store" {
		t.Fatal(out)
	}
	var body map[string]any
	if e := json.Unmarshal([]byte(out.Body), &body); e != nil || len(body) != 7 {
		t.Fatal("public contract leaked fields", body, e)
	}
	for _, r := range []readResult{{err: errors.New("db unavailable")}, {config: Default("prod")}, {config: Config{SchemaVersion: 2, Environment: "dev"}}} {
		handler.Store = r
		out, _ = handler.Handle(context.Background(), req)
		if out.StatusCode != 503 {
			t.Fatal("invalid/unavailable config enabled fallback", out)
		}
	}
	req.RequestContext.HTTP.Method = "POST"
	out, _ = handler.Handle(context.Background(), req)
	if out.StatusCode != 404 {
		t.Fatal(out)
	}
}
