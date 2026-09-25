package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

type Handler struct{ Service *email.Service }

func response(code int, v any) events.APIGatewayV2HTTPResponse {
	b, _ := json.Marshal(v)
	return events.APIGatewayV2HTTPResponse{StatusCode: code, Headers: map[string]string{"content-type": "application/json; charset=utf-8", "cache-control": "no-store", "referrer-policy": "no-referrer", "x-content-type-options": "nosniff"}, Body: string(b)}
}
func failure(err error) events.APIGatewayV2HTTPResponse {
	switch {
	case errors.Is(err, email.ErrInvalid):
		return response(400, map[string]string{"error": "invalid_request"})
	case errors.Is(err, email.ErrIncorrect):
		var wrong email.IncorrectCode
		if errors.As(err, &wrong) {
			return response(422, map[string]any{"error": "incorrect_code", "attempts_remaining": wrong.Remaining})
		}
		return response(422, map[string]string{"error": "incorrect_code"})
	case errors.Is(err, email.ErrLimited):
		return response(429, map[string]string{"error": "attempt_limit"})
	case errors.Is(err, email.ErrUnusable):
		return response(410, map[string]string{"error": "link_unusable"})
	case errors.Is(err, email.ErrUsed), errors.Is(err, email.ErrSignInRequired):
		return response(409, map[string]string{"error": "confirmed_sign_in_required"})
	case errors.Is(err, email.ErrConflict):
		return response(409, map[string]string{"error": "confirmation_in_progress"})
	default:
		return response(503, map[string]string{"error": "temporarily_unavailable"})
	}
}
func decode(req events.APIGatewayV2HTTPRequest, v any) error {
	body := req.Body
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return email.ErrInvalid
		}
		body = string(b)
	}
	if len(body) > 4096 {
		return email.ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return email.ErrInvalid
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return email.ErrInvalid
	}
	return nil
}
func onRoute(req events.APIGatewayV2HTTPRequest, path string) bool {
	return req.RequestContext.HTTP.Method == "POST" && req.RawPath == path
}

func (h Handler) Signup(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !onRoute(req, "/signup") {
		return response(404, map[string]string{"error": "not_found"}), nil
	}
	var v struct {
		Email     string `json:"email"`
		Challenge string `json:"code_challenge"`
		Method    string `json:"code_challenge_method"`
	}
	if err := decode(req, &v); err != nil {
		return failure(err), nil
	}
	result, err := h.Service.Signup(ctx, v.Email, v.Challenge, v.Method, req.RequestContext.HTTP.SourceIP)
	if err != nil {
		return failure(err), nil
	}
	return response(202, result), nil
}

func (h Handler) Resend(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !onRoute(req, "/resend") {
		return response(404, map[string]string{"error": "not_found"}), nil
	}
	var v struct {
		RequestID string `json:"request_id"`
	}
	if err := decode(req, &v); err != nil {
		return failure(err), nil
	}
	if err := h.Service.Resend(ctx, v.RequestID, req.RequestContext.HTTP.SourceIP); err != nil {
		return failure(err), nil
	}
	return response(202, map[string]string{"status": "accepted"}), nil
}

func (h Handler) Confirm(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if !onRoute(req, "/confirm") {
		return response(404, map[string]string{"error": "not_found"}), nil
	}
	var v email.ConfirmInput
	if err := decode(req, &v); err != nil {
		return failure(err), nil
	}
	session, err := h.Service.Confirm(ctx, v)
	if err != nil {
		return failure(err), nil
	}
	return response(200, session), nil
}
