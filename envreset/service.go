// Package envreset owns destructive environment-data reset, never infrastructure teardown.
package envreset

import (
	"context"
	"errors"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"time"
)

var ErrInvalid = errors.New("invalid_reset_request")
var ErrDisabled = errors.New("reset_disabled")
var ErrConflict = errors.New("reset_in_progress")

type Request struct {
	Action       string `json:"action"`
	Environment  string `json:"environment"`
	Confirmation string `json:"confirmation"`
	Operation    string `json:"operation"`
	Actor        string `json:"actor"`
}
type State struct {
	Revision    int64          `json:"revision"`
	Operation   string         `json:"operation"`
	Actor       string         `json:"actor"`
	Phase       string         `json:"phase"`
	Error       string         `json:"error,omitempty"`
	StartedAt   int64          `json:"startedAt"`
	UpdatedAt   int64          `json:"updatedAt"`
	Due         int64          `json:"due"`
	ValidAfter  int64          `json:"-"`
	LeaseUntil  int64          `json:"-"`
	Concurrency map[string]int `json:"-"`
}
type Status struct {
	Enabled     bool   `json:"enabled"`
	Environment string `json:"environment"`
	State       State  `json:"state"`
}
type Store interface {
	Load(context.Context) (State, error)
	Save(context.Context, State, State) error
}
type Backend interface {
	Snapshot(context.Context) (map[string]int, error)
	Pause(context.Context, map[string]int) error
	Purge(context.Context) error
	Clear(context.Context, string) (bool, error)
	Restore(context.Context, map[string]int) error
}
type Service struct {
	Store       Store
	Backend     Backend
	Environment string
	Enabled     bool
	Now         func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s Service) Status(ctx context.Context) (Status, error) {
	r, e := s.Store.Load(ctx)
	return Status{s.Enabled, s.Environment, r}, e
}
func (s Service) Start(ctx context.Context, q Request) (Status, error) {
	if !s.Enabled {
		return Status{}, ErrDisabled
	}
	if q.Environment != s.Environment || q.Confirmation != s.Environment || !capture.ValidOperation(q.Operation) || q.Actor == "" || len(q.Actor) > 128 {
		return Status{}, ErrInvalid
	}
	r, e := s.Store.Load(ctx)
	if e != nil {
		return Status{}, e
	}
	if r.Operation == q.Operation {
		return Status{s.Enabled, s.Environment, r}, nil
	}
	if r.Phase != "" && r.Phase != "complete" {
		return Status{}, ErrConflict
	}
	next := State{Revision: r.Revision + 1, Operation: q.Operation, Actor: q.Actor, Phase: "snapshot", StartedAt: s.now().Unix(), UpdatedAt: s.now().Unix(), ValidAfter: r.ValidAfter}
	if e = s.Store.Save(ctx, r, next); e != nil {
		return Status{}, e
	}
	return Status{s.Enabled, s.Environment, next}, nil
}
func (s Service) Resume(ctx context.Context, q Request) (Status, error) {
	if !s.Enabled {
		return Status{}, ErrDisabled
	}
	r, e := s.Store.Load(ctx)
	if e != nil {
		return Status{}, e
	}
	if q.Environment != s.Environment || q.Confirmation != s.Environment || q.Operation != r.Operation || q.Actor == "" || r.Error == "" || r.LeaseUntil > s.now().Unix() {
		return Status{}, ErrInvalid
	}
	next := r
	next.Error = ""
	next.Revision++
	next.UpdatedAt = s.now().Unix()
	if e = s.Store.Save(ctx, r, next); e != nil {
		return Status{}, e
	}
	return Status{s.Enabled, s.Environment, next}, nil
}

// One leased, restart-safe phase per invocation; deleting a page is idempotent.
// Persist original concurrency BEFORE any function is paused. Never resume on failure.
func (s Service) Work(ctx context.Context) error {
	r, e := s.Store.Load(ctx)
	if e != nil {
		return e
	}
	now := s.now().Unix()
	if r.Phase == "" || r.Phase == "complete" || r.Error != "" || r.LeaseUntil > now || r.Due > now {
		return nil
	}
	claimed := r
	claimed.Revision++
	claimed.LeaseUntil = now + 90
	if e = s.Store.Save(ctx, r, claimed); e != nil {
		return e
	}
	r = claimed
	next := r
	switch r.Phase {
	case "snapshot":
		next.Concurrency, e = s.Backend.Snapshot(ctx)
		if e == nil {
			next.Phase = "pause"
		}
	case "pause":
		e = s.Backend.Pause(ctx, r.Concurrency)
		if e == nil {
			next.Phase = "drain"
			next.Due = now + 420
		}
	case "drain":
		next.Phase = "purge"
	case "purge":
		e = s.Backend.Purge(ctx)
		if e == nil {
			next.Phase = "queue_wait"
			next.Due = now + 60
		}
	case "queue_wait":
		next.Phase = "users"
	case "users", "tables", "objects":
		var done bool
		done, e = s.Backend.Clear(ctx, r.Phase)
		if e == nil && done {
			next.Phase = map[string]string{"users": "tables", "tables": "objects", "objects": "restore"}[r.Phase]
			if next.Phase == "restore" {
				next.ValidAfter = now
			}
		}
	case "restore":
		e = s.Backend.Restore(ctx, r.Concurrency)
		if e == nil {
			next.Phase = "complete"
		}
	default:
		e = ErrInvalid
	}
	next.Revision++
	next.LeaseUntil = 0
	next.UpdatedAt = s.now().Unix()
	if e != nil {
		if r.Phase == "restore" {
			_ = s.Backend.Pause(ctx, r.Concurrency)
			next.Phase = "pause"
		}
		next.Error = "reset_step_failed"
	}
	return s.Store.Save(ctx, r, next)
}
