package email

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSafeSESDenialRedactsProofsAddressesAndNewlines(t *testing.T) {
	const raw = "Denied verify@info.attestrabond.com and asd@isdavid.com at https://app.example.com/verify-email?b=secret\ntrace: ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678901234567890"
	got := safeSESDenial(raw)
	for _, secret := range []string{"verify@", "asd@", "https://", "secret", "ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678901234567890", "\n"} {
		if strings.Contains(got, secret) {
			t.Fatalf("unsafe log: %q", got)
		}
	}
}

type fakeStore struct {
	t      Transaction
	exists bool
	budget map[string]int
}

func (s *fakeStore) Put(_ context.Context, t Transaction) error { s.t = t; s.exists = true; return nil }
func (s *fakeStore) Get(_ context.Context, id string) (Transaction, error) {
	if !s.exists || s.t.ID != id {
		return Transaction{}, ErrNotFound
	}
	return s.t, nil
}
func (s *fakeStore) Swap(_ context.Context, before, after Transaction) error {
	if s.t.Version != before.Version {
		return ErrConflict
	}
	s.t = after
	return nil
}
func (s *fakeStore) Charge(_ context.Context, name string, limit int, _ time.Time) error {
	if s.budget == nil {
		s.budget = map[string]int{}
	}
	if s.budget[name] >= limit {
		return ErrLimited
	}
	s.budget[name]++
	return nil
}

type fakeSender struct {
	b, c, link string
	count      int
	err        error
}

func (s *fakeSender) Send(_ context.Context, _, link, code string) error {
	s.link = link
	s.c = code
	s.count++
	return s.err
}

type fakeIdentity struct {
	created, issued       int
	failIssue, failCreate bool
}

func (i *fakeIdentity) Eligible(_ context.Context, _ string) (bool, error) {
	return i.created == 0, nil
}
func (i *fakeIdentity) Confirm(_ context.Context, _ string) (string, error) {
	if i.failCreate {
		return "", errors.New("provider down")
	}
	i.created++
	return "sub-1", nil
}
func (i *fakeIdentity) Session(_ context.Context, _ string) (Session, error) {
	i.issued++
	if i.failIssue {
		return Session{}, errors.New("provider down")
	}
	return Session{AccessToken: "access"}, nil
}

func fixture() (*Service, *fakeStore, *fakeSender, *fakeIdentity, string) {
	store := &fakeStore{}
	send := &fakeSender{}
	identity := &fakeIdentity{}
	s := &Service{Store: store, Sender: send, Identity: identity, Key: []byte("12345678901234567890123456789012"), Origin: "https://app.example.com", Now: func() time.Time { return time.Unix(1700000000, 0) }, Random: func(b []byte) error {
		for n := range b {
			b[n] = byte(n + 1)
		}
		return nil
	}}
	a := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyzABCDEF"))
	return s, store, send, identity, a
}
func challenge(a string) string {
	raw, _ := base64.RawURLEncoding.DecodeString(a)
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestLinkDoesNotRevealCodeOrConfirm(t *testing.T) {
	s, store, send, id, a := fixture()
	ctx := context.Background()
	r, err := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.RequestID) == 0 || send.count != 1 || send.c == "" {
		t.Fatalf("signup: %+v / %+v", r, send)
	}
	if contains(send.link, send.c) || id.created != 0 || store.t.State != Pending {
		t.Fatal("GET link or signup consumed proof or exposed C")
	}
}
func TestSignupDeliveryFailureIsVisibleInDiagnostics(t *testing.T) {
	s, _, send, _, a := fixture()
	s.Diagnostics = true
	send.err = errors.New("provider says: me@example.com, secret verification code")
	_, err := s.Signup(context.Background(), "me@example.com", challenge(a), "S256", "source")
	var operational *OperationalError
	if !errors.As(err, &operational) || operational.Stage != "ses_send" {
		t.Fatalf("want ses_send diagnostic, got %v", err)
	}
}
func TestSignupDeliveryFailureIsMaskedByDefault(t *testing.T) {
	s, _, send, _, a := fixture()
	send.err = errors.New("SES unavailable")
	_, err := s.Signup(context.Background(), "me@example.com", challenge(a), "S256", "source")
	if err != nil {
		t.Fatalf("default signup should mask delivery failure: %v", err)
	}
}
func token(link string) string { u, _ := url.Parse(link); return u.Query().Get("b") }
func TestAutoProofRequiresMatchingAAndB(t *testing.T) {
	s, store, send, id, a := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	b := token(send.link)
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), TokenA: a}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("wrong B: %v", err)
	}
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: b, TokenA: "wrong"}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("wrong A: %v", err)
	}
	if store.t.Attempts != 0 {
		t.Fatal("wrong A charged C attempts")
	}
	result, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: b, TokenA: a})
	if err != nil || result.AccessToken != "access" {
		t.Fatalf("confirm: %+v %v", result, err)
	}
	if id.issued != 1 || store.t.State != Confirmed {
		t.Fatal("session not exclusive")
	}
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: b, TokenA: a}); !errors.Is(err, ErrUsed) {
		t.Fatalf("replay: %v", err)
	}
}
func TestManualCodeIsSixDigitsAndLimited(t *testing.T) {
	s, store, send, _, _ := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge("QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE"), "S256", "source")
	b := token(send.link)
	if len(send.c) != 6 {
		t.Fatalf("code %q", send.c)
	}
	for i := 0; i < 5; i++ {
		_, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: b, TokenC: "999999"})
		if !errors.Is(err, ErrIncorrect) && !errors.Is(err, ErrLimited) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if store.t.Attempts != 5 {
		t.Fatalf("attempts %d", store.t.Attempts)
	}
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: b, TokenC: send.c}); !errors.Is(err, ErrLimited) {
		t.Fatalf("exhausted: %v", err)
	}
}
func TestResendRotatesBAndCButKeepsChallenge(t *testing.T) {
	s, store, send, _, a := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	old := store.t
	oldB := token(send.link)
	s.Now = func() time.Time { return time.Unix(1700000061, 0) }
	s.Random = func(b []byte) error {
		for n := range b {
			b[n] = byte(100 + n)
		}
		return nil
	}
	if err := s.Resend(ctx, r.RequestID, "source"); err != nil {
		t.Fatal(err)
	}
	if store.t.Generation != 2 || store.t.Challenge != old.Challenge || store.t.BHash == old.BHash || send.count != 2 || send.c != "000000" {
		t.Fatal("resend did not rotate and retain binding or leading zeroes")
	}
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: oldB, TokenA: a}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("old link: %v", err)
	}
}
func TestSessionFailureClosesProof(t *testing.T) {
	s, store, send, id, a := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	id.failIssue = true
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: token(send.link), TokenA: a}); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("failure: %v", err)
	}
	if store.t.State != Confirmed || id.issued != 1 {
		t.Fatal("provider failure reopened proof")
	}
}
func TestInterruptedClaimCanReconcileAfterTimeout(t *testing.T) {
	s, store, send, _, a := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	stuck := store.t
	stuck.State = Confirming
	stuck.ClaimedAt = s.now().Unix()
	stuck.Version++
	store.t = stuck
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: token(send.link), TokenA: a}); !errors.Is(err, ErrConflict) {
		t.Fatalf("early retry: %v", err)
	}
	s.Now = func() time.Time { return time.Unix(stuck.ClaimedAt+61, 0) }
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: token(send.link), TokenA: a}); err != nil {
		t.Fatalf("reconciled: %v", err)
	}
	if store.t.State != Confirmed {
		t.Fatal("not confirmed")
	}
}
func TestProviderCreationFailureDoesNotClaimAccountWasConfirmed(t *testing.T) {
	s, store, send, id, a := fixture()
	ctx := context.Background()
	r, _ := s.Signup(ctx, "me@example.com", challenge(a), "S256", "source")
	id.failCreate = true
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: token(send.link), TokenA: a}); !errors.Is(err, ErrUnusable) {
		t.Fatalf("creation failure: %v", err)
	}
	if store.t.State != Failed {
		t.Fatal("failed transaction reopened")
	}
	s.Now = func() time.Time { return time.Unix(1700000061, 0) }
	s.Random = func(b []byte) error {
		for n := range b {
			b[n] = byte(100 + n)
		}
		return nil
	}
	if err := s.Resend(ctx, r.RequestID, "source"); err != nil {
		t.Fatal(err)
	}
	if store.t.State != Pending || store.t.Generation != 2 || send.count != 2 {
		t.Fatal("failed proof was not replaced")
	}
	id.failCreate = false
	if _, err := s.Confirm(ctx, ConfirmInput{RequestID: r.RequestID, TokenB: token(send.link), TokenA: a}); err != nil {
		t.Fatalf("replacement proof: %v", err)
	}
}
func contains(s, part string) bool { return strings.Contains(s, part) }
