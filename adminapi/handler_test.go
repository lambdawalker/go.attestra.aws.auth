package adminapi

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"strings"
	"testing"
)

type repository struct {
	config       runtimeconfig.Config
	grants       []runtimeconfig.Grant
	published    []runtimeconfig.Version
	publishError error
}

func (r *repository) Get(context.Context, string) (runtimeconfig.Config, error) { return r.config, nil }
func (r *repository) Grants(context.Context, string) ([]runtimeconfig.Grant, error) {
	return r.grants, nil
}
func (r *repository) History(context.Context, string) ([]runtimeconfig.Version, error) {
	return r.published, nil
}
func (r *repository) GetVersion(_ context.Context, _ string, rev int64) (runtimeconfig.Version, error) {
	for _, v := range r.published {
		if v.Config.Revision == rev {
			return v, nil
		}
	}
	return runtimeconfig.Version{}, runtimeconfig.ErrNotFound
}
func (r *repository) Publish(_ context.Context, _ runtimeconfig.Grant, _ int64, v runtimeconfig.Version) error {
	if r.publishError != nil {
		return r.publishError
	}
	r.published = append(r.published, v)
	return nil
}
func (r *repository) SetGrant(_ context.Context, actor, target runtimeconfig.Grant) error {
	if !actor.ManageGrants || actor.Environment != target.Environment {
		return runtimeconfig.ErrForbidden
	}
	return nil
}
func setup() (Handler, *repository) {
	r := &repository{config: runtimeconfig.Default("dev"), grants: []runtimeconfig.Grant{{Subject: "admin", Environment: "dev", Active: true, Revision: 1}}}
	return Handler{Service: runtimeconfig.Service{Store: r}, Issuer: "https://cognito.example/admin", ClientID: "admin-client", Origin: "https://admin.example", LoginDomain: "https://login.auth.us-east-2.amazoncognito.com"}, r
}
func request(h Handler, method, path string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{RawPath: path, Headers: map[string]string{"origin": h.Origin}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: method}, Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{Claims: map[string]string{"sub": "admin", "iss": h.Issuer, "client_id": h.ClientID, "token_use": "access"}}}}}
}
func TestAdminIdentityBoundaryAndPerEnvironmentGrant(t *testing.T) {
	h, _ := setup()
	ctx := context.Background()
	for _, claim := range []string{"iss", "client_id", "token_use", "sub"} {
		req := request(h, "GET", "/api/environments/dev")
		req.RequestContext.Authorizer.JWT.Claims[claim] = "wrong"
		if claim == "sub" {
			req.RequestContext.Authorizer.JWT.Claims[claim] = ""
		}
		out, _ := h.Handle(ctx, req)
		if out.StatusCode != 401 {
			t.Fatalf("%s accepted: %d", claim, out.StatusCode)
		}
	}
	req := request(h, "GET", "/api/environments/dev")
	req.RequestContext.Authorizer = nil
	out, _ := h.Handle(ctx, req)
	if out.StatusCode != 401 {
		t.Fatal(out)
	}
	out, _ = h.Handle(ctx, request(h, "GET", "/api/environments/prod"))
	if out.StatusCode != 403 {
		t.Fatal("cross-env access", out)
	}
	out, _ = h.Handle(ctx, request(h, "GET", "/api/environments/dev"))
	if out.StatusCode != 200 {
		t.Fatal(out)
	}
}
func TestPublishStrictValidationOriginConcurrencyAndRollback(t *testing.T) {
	h, r := setup()
	ctx := context.Background()
	req := request(h, "POST", "/api/environments/dev")
	body := map[string]any{"expectedRevision": int64(0), "config": r.config}
	b, _ := json.Marshal(body)
	req.Body = string(b)
	req.Headers["origin"] = "https://evil.example"
	out, _ := h.Handle(ctx, req)
	if out.StatusCode != 403 || len(r.published) > 0 {
		t.Fatal("cross-origin publish accepted")
	}
	req.Headers["origin"] = h.Origin
	req.Body = strings.TrimSuffix(string(b), "}") + `,"secret":"oops"}`
	out, _ = h.Handle(ctx, req)
	if out.StatusCode != 400 {
		t.Fatal("unknown fields accepted", out)
	}
	req.Body = string(b)
	r.publishError = runtimeconfig.ErrConflict
	out, _ = h.Handle(ctx, req)
	if out.StatusCode != 409 || !strings.Contains(out.Body, "revision_conflict") {
		t.Fatal(out)
	}
	r.publishError = nil
	out, _ = h.Handle(ctx, req)
	if out.StatusCode != 200 || len(r.published) != 1 || r.published[0].Actor != "admin" {
		t.Fatal(out)
	}
	old := runtimeconfig.Default("dev")
	r.published = append(r.published, runtimeconfig.Version{Config: old, Action: "defaults"})
	req.Body = `{"expectedRevision":1,"rollbackRevision":0,"config":{"environment":"dev"}}`
	out, _ = h.Handle(ctx, req)
	if out.StatusCode != 200 || r.published[len(r.published)-1].Action != "rollback" || r.published[len(r.published)-1].Config.Revision != 2 {
		t.Fatal(out)
	}
}
func TestInactiveGrantAndManagerOnlyOperations(t *testing.T) {
	h, r := setup()
	r.grants[0].Active = false
	out, _ := h.Handle(context.Background(), request(h, "GET", "/api/environments/dev"))
	if out.StatusCode != 403 {
		t.Fatal(out)
	}
	r.grants[0].Active = true
	req := request(h, "GET", "/api/environments/dev/grants")
	req.QueryStringParameters = map[string]string{"subject": "other"}
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 403 {
		t.Fatal(out)
	}
	r.grants[0].ManageGrants = true
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 200 {
		t.Fatal(out)
	}
}
func TestConsoleSecurityAndNoInlineExecutableAssets(t *testing.T) {
	h, _ := setup()
	for _, path := range []string{"/", "/callback", "/app.js", "/style.css"} {
		req := request(h, "GET", path)
		req.RequestContext.Authorizer = nil
		out, _ := h.Handle(context.Background(), req)
		if out.StatusCode != 200 || !strings.Contains(out.Headers["content-security-policy"], "frame-ancestors 'none'") || out.Headers["cache-control"] != "no-store" {
			t.Fatal(path, out)
		}
	}
	if strings.Contains(index, "onclick=") || strings.Contains(index, "<script>") || strings.Contains(script, "localStorage") || strings.Contains(script, "sessionStorage.setItem('access") {
		t.Fatal("unsafe token persistence or inline script")
	}
	if !strings.Contains(script, "/passkeys/add") || !strings.Contains(script, `code_challenge_method: "S256"`) {
		t.Fatal("enrollment or PKCE missing")
	}
}
