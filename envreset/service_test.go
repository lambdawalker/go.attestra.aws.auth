package envreset

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mem struct{ state State }

func (m *mem) Load(context.Context) (State, error) { return m.state, nil }
func (m *mem) Save(_ context.Context, old, next State) error {
	if m.state.Revision != old.Revision {
		return ErrConflict
	}
	m.state = next
	return nil
}

type fake struct {
	calls       []string
	fail        bool
	restoreFail bool
}

func (f *fake) Snapshot(context.Context) (map[string]int, error) {
	f.calls = append(f.calls, "snapshot")
	return map[string]int{"app": -1, "worker": 2}, nil
}
func (f *fake) Pause(context.Context, map[string]int) error {
	f.calls = append(f.calls, "pause")
	return nil
}
func (f *fake) Purge(context.Context) error { f.calls = append(f.calls, "purge"); return nil }
func (f *fake) Clear(_ context.Context, kind string) (bool, error) {
	f.calls = append(f.calls, kind)
	if f.fail {
		return false, errors.New("private provider message")
	}
	return true, nil
}
func (f *fake) Restore(context.Context, map[string]int) error {
	f.calls = append(f.calls, "restore")
	if f.restoreFail {
		return errors.New("restore failed")
	}
	return nil
}
func TestResetFencesDrainAndCompletes(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &mem{}
	f := &fake{}
	s := Service{Store: m, Backend: f, Environment: "dev", Enabled: true, Now: func() time.Time { return now }}
	ctx := context.Background()
	request := Request{Action: "start", Environment: "dev", Confirmation: "dev", Operation: "reset_operation_123", Actor: "admin"}
	if _, e := s.Start(ctx, request); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Start(ctx, request); e != nil {
		t.Fatal("lost response retry", e)
	}
	if e := s.Work(ctx); e != nil {
		t.Fatal(e)
	}
	if m.state.Phase != "pause" {
		t.Fatal(m.state.Phase)
	}
	if e := s.Work(ctx); e != nil {
		t.Fatal(e)
	}
	if m.state.Phase != "drain" {
		t.Fatal(m.state.Phase)
	}
	s.Work(ctx)
	if len(f.calls) != 2 {
		t.Fatal("deleted before drain")
	}
	now = now.Add(7 * time.Minute)
	s.Work(ctx)
	s.Work(ctx)
	if m.state.Phase != "queue_wait" {
		t.Fatal(m.state.Phase)
	}
	now = now.Add(time.Minute)
	for i := 0; i < 12 && m.state.Phase != "complete"; i++ {
		if e := s.Work(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if m.state.Phase != "complete" || f.calls[len(f.calls)-1] != "restore" || m.state.ValidAfter != now.Unix() {
		t.Fatal(m.state)
	}
}
func TestResetRequiresExactConfirmationAndFailureStaysPaused(t *testing.T) {
	m := &mem{}
	f := &fake{}
	s := Service{Store: m, Backend: f, Environment: "dev", Enabled: true}
	q := Request{Action: "start", Environment: "prod", Confirmation: "prod", Operation: "reset_operation_123", Actor: "admin"}
	if _, e := s.Start(context.Background(), q); e != ErrInvalid {
		t.Fatal(e)
	}
	q.Environment = "dev"
	q.Confirmation = "DEV"
	if _, e := s.Start(context.Background(), q); e != ErrInvalid {
		t.Fatal(e)
	}
	q.Confirmation = "dev"
	s.Enabled = false
	if _, e := s.Start(context.Background(), q); e != ErrDisabled {
		t.Fatal(e)
	}
	s.Enabled = true
	s.Start(context.Background(), q)
	m.state.Phase = "tables"
	f.fail = true
	s.Work(context.Background())
	if m.state.Error != "reset_step_failed" || m.state.Phase != "tables" {
		t.Fatal("failed open")
	}
	for _, c := range f.calls {
		if c == "restore" {
			t.Fatal("restored on failure")
		}
	}
	q.Operation = "another_operation_123"
	if _, e := s.Start(context.Background(), q); e != ErrConflict {
		t.Fatal("replaced active operation", e)
	}
}

func TestPartialRestoreRepausesAndResumeDrainsAgain(t *testing.T) {
	m := &mem{state: State{Revision: 1, Operation: "reset_operation_123", Phase: "restore", Concurrency: map[string]int{"app": -1}}}
	f := &fake{restoreFail: true}
	s := Service{Store: m, Backend: f, Enabled: true, Environment: "dev"}
	if err := s.Work(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.state.Phase != "pause" || m.state.Error == "" || len(f.calls) != 2 || f.calls[1] != "pause" {
		t.Fatal(m.state, f.calls)
	}
	_, err := s.Resume(context.Background(), Request{Environment: "dev", Confirmation: "dev", Operation: m.state.Operation, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Work(context.Background()); err != nil || m.state.Phase != "drain" {
		t.Fatal(m.state, err)
	}
}
func TestActiveLeasePreventsConcurrentWork(t *testing.T) {
	m := &mem{state: State{Revision: 1, Phase: "tables", LeaseUntil: 200}}
	f := &fake{}
	s := Service{Store: m, Backend: f, Now: func() time.Time { return time.Unix(100, 0) }}
	if err := s.Work(context.Background()); err != nil || len(f.calls) != 0 {
		t.Fatal(err, f.calls)
	}
}
