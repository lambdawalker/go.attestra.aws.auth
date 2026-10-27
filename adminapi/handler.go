// Package adminapi serves a same-origin admin console and separately authorized management API.
package adminapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/aws/aws-lambda-go/events"
	"github.com/lambdawalker/go.attestra.aws.auth/passkey"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"io"
	"strings"
)

type Handler struct {
	Passkeys                              *PasskeyMFA
	Service                               runtimeconfig.Service
	Issuer, ClientID, Origin, LoginDomain string
}

func reply(status int, v any) events.APIGatewayV2HTTPResponse {
	b, _ := json.Marshal(v)
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store", "x-content-type-options": "nosniff"}, Body: string(b)}
}
func failure(e error) events.APIGatewayV2HTTPResponse {
	code, status := "service_unavailable", 503
	switch {
	case errors.Is(e, runtimeconfig.ErrInvalid):
		code, status = "invalid_config", 400
	case errors.Is(e, runtimeconfig.ErrForbidden):
		code, status = "environment_forbidden", 403
	case errors.Is(e, runtimeconfig.ErrConflict):
		code, status = "revision_conflict", 409
	case errors.Is(e, runtimeconfig.ErrNotFound):
		code, status = "not_found", 404
	}
	return reply(status, map[string]string{"error": code})
}
func decode(req events.APIGatewayV2HTTPRequest, v any) error {
	b := req.Body
	if len(b) > 16000 {
		return runtimeconfig.ErrInvalid
	}
	if req.IsBase64Encoded {
		raw, e := base64.StdEncoding.DecodeString(b)
		if e != nil {
			return runtimeconfig.ErrInvalid
		}
		b = string(raw)
	}
	d := json.NewDecoder(strings.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return runtimeconfig.ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return runtimeconfig.ErrInvalid
	}
	return nil
}
func (h Handler) Handle(ctx context.Context, r events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	method, path := r.RequestContext.HTTP.Method, r.RawPath
	if method == "GET" {
		switch path {
		case "/", "/callback":
			return asset("text/html; charset=utf-8", index), nil
		case "/app.js":
			return asset("text/javascript; charset=utf-8", script), nil
		case "/style.css":
			return asset("text/css; charset=utf-8", style), nil
		case "/settings":
			return reply(200, map[string]string{"issuer": h.Issuer, "clientId": h.ClientID, "origin": h.Origin, "loginDomain": h.LoginDomain}), nil
		}
	}
	// API Gateway verifies signature and expiration; this independent issuer/client check prevents end-user token confusion.
	if r.RequestContext.Authorizer == nil || r.RequestContext.Authorizer.JWT == nil {
		return reply(401, map[string]string{"error": "admin_sign_in_required"}), nil
	}
	c := r.RequestContext.Authorizer.JWT.Claims
	sub := c["sub"]
	if sub == "" || c["iss"] != h.Issuer || c["client_id"] != h.ClientID || c["token_use"] != "access" {
		return reply(401, map[string]string{"error": "admin_sign_in_required"}), nil
	}
	if method != "GET" && r.Headers["origin"] != h.Origin {
		return failure(runtimeconfig.ErrForbidden), nil
	}
	if method == "POST" && path == "/api/passkey/activate" {
		var in struct{}
		if e := decode(r, &in); e != nil {
			return failure(e), nil
		}
		if h.Passkeys == nil {
			return failure(errors.New("unavailable")), nil
		}
		token, e := passkey.Bearer(r.Headers["authorization"])
		if e != nil {
			return reply(401, map[string]string{"error": "admin_sign_in_required"}), nil
		}
		if e = h.Passkeys.Activate(ctx, token); e != nil {
			if errors.Is(e, ErrPasskeyNotReady) {
				return reply(409, map[string]string{"error": ErrPasskeyNotReady.Error()}), nil
			}
			return failure(e), nil
		}
		return reply(200, map[string]bool{"activated": true}), nil
	}
	if method == "GET" && path == "/api/environments" {
		gs, e := h.Service.Store.Grants(ctx, sub)
		if e != nil {
			return failure(e), nil
		}
		active := []runtimeconfig.Grant{}
		for _, g := range gs {
			if g.Active {
				active = append(active, g)
			}
		}
		return reply(200, active), nil
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/environments/"), "/")
	if !strings.HasPrefix(path, "/api/environments/") || len(p) > 2 || !runtimeconfig.ValidEnvironment(p[0]) {
		return failure(runtimeconfig.ErrNotFound), nil
	}
	env := p[0]
	g, e := h.Service.Authorized(ctx, sub, env)
	if e != nil {
		return failure(e), nil
	}
	if method == "GET" && len(p) == 1 {
		config, e := h.Service.Store.Get(ctx, env)
		if e != nil {
			return failure(e), nil
		}
		return reply(200, config), nil
	}
	if method == "GET" && len(p) == 2 && p[1] == "history" {
		v, e := h.Service.Store.History(ctx, env)
		if e != nil {
			return failure(e), nil
		}
		return reply(200, v), nil
	}
	if method == "POST" && len(p) == 1 {
		var in struct {
			ExpectedRevision int64                `json:"expectedRevision"`
			Config           runtimeconfig.Config `json:"config"`
			RollbackRevision *int64               `json:"rollbackRevision,omitempty"`
		}
		if e := decode(r, &in); e != nil {
			return failure(e), nil
		}
		if in.ExpectedRevision < 0 {
			return failure(runtimeconfig.ErrInvalid), nil
		}
		if in.RollbackRevision != nil {
			version, e := h.Service.Store.GetVersion(ctx, env, *in.RollbackRevision)
			if e != nil {
				return failure(e), nil
			}
			in.Config = version.Config

		}
		if in.Config.Environment != env {
			return failure(runtimeconfig.ErrInvalid), nil
		}
		out, e := h.Service.Publish(ctx, g, in.ExpectedRevision, in.Config, in.RollbackRevision)
		if e != nil {
			return failure(e), nil
		}
		return reply(200, out), nil
	}
	if method == "GET" && len(p) == 2 && p[1] == "grants" {
		if !g.ManageGrants {
			return failure(runtimeconfig.ErrForbidden), nil
		}
		subject := r.QueryStringParameters["subject"]
		if subject == "" || len(subject) > 100 {
			return failure(runtimeconfig.ErrInvalid), nil
		}
		grants, e := h.Service.Store.Grants(ctx, subject)
		if e != nil {
			return failure(e), nil
		}
		for _, target := range grants {
			if target.Environment == env {
				return reply(200, target), nil
			}
		}
		return reply(200, runtimeconfig.Grant{Subject: subject, Environment: env}), nil
	}
	if method == "POST" && len(p) == 2 && p[1] == "grants" {
		var target runtimeconfig.Grant
		if e := decode(r, &target); e != nil {
			return failure(e), nil
		}
		if target.Environment != env {
			return failure(runtimeconfig.ErrInvalid), nil
		}
		if e := h.Service.Store.SetGrant(ctx, g, target); e != nil {
			return failure(e), nil
		}
		target.Revision++
		return reply(200, target), nil
	}
	return failure(runtimeconfig.ErrNotFound), nil
}
func asset(kind, body string) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{StatusCode: 200, Headers: map[string]string{"content-type": kind, "cache-control": "no-store", "content-security-policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self' https://*.amazoncognito.com; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'", "strict-transport-security": "max-age=31536000; includeSubDomains", "x-content-type-options": "nosniff", "referrer-policy": "no-referrer"}, Body: body}
}
