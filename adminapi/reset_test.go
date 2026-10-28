package adminapi

import (
	"context"
	"github.com/lambdawalker/go.attestra.aws.auth/envreset"
	"testing"
)

type resetRecorder struct{ calls []envreset.Request }

func (f *resetRecorder) Invoke(_ context.Context, q envreset.Request) (envreset.Status, error) {
	f.calls = append(f.calls, q)
	return envreset.Status{Enabled: true, Environment: q.Environment, State: envreset.State{Phase: "snapshot"}}, nil
}
func TestResetRequiresManagerOriginAndExactScope(t *testing.T) {
	h, repo := setup()
	reset := &resetRecorder{}
	h.Reset = reset
	req := request(h, "POST", "/api/environments/dev/reset")
	req.Body = `{"action":"start","confirmation":"dev","operation":"reset_operation_123"}`
	out, _ := h.Handle(context.Background(), req)
	if out.StatusCode != 403 || len(reset.calls) != 0 {
		t.Fatal("nonmanager reset")
	}
	repo.grants[0].ManageGrants = true
	req.Headers["origin"] = "https://evil.example"
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 403 {
		t.Fatal("cross origin reset")
	}
	req.Headers["origin"] = h.Origin
	req.Body = `{"action":"start","confirmation":"prod","operation":"reset_operation_123"}`
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 400 {
		t.Fatal("confirmation mismatch")
	}
	req.Body = `{"action":"start","confirmation":"dev","operation":"reset_operation_123","pool":"admin-pool"}`
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 400 {
		t.Fatal("resource injection")
	}
	req.Body = `{"action":"start","confirmation":"dev","operation":"reset_operation_123"}`
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 202 || len(reset.calls) != 1 || reset.calls[0].Actor != "admin" || reset.calls[0].Environment != "dev" {
		t.Fatal(out)
	}
	req.RawPath = "/api/environments/prod/reset"
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 403 || len(reset.calls) != 1 {
		t.Fatal("cross environment reset")
	}
}
