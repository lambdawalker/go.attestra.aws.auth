package email

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/aws/smithy-go"
)

var (
	ErrInvalid        = errors.New("invalid request")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrLimited        = errors.New("attempt limit")
	ErrUnusable       = errors.New("link unusable")
	ErrIncorrect      = errors.New("incorrect code")
	ErrUsed           = errors.New("already confirmed")
	ErrSignInRequired = errors.New("confirmed; sign in required")
)

type State string

const (
	Pending    State = "pending"
	Confirming State = "confirming"
	Confirmed  State = "confirmed"
	Failed     State = "failed"
)

type Transaction struct {
	ID         string `dynamodbav:"id"`
	Email      string `dynamodbav:"email"`
	Challenge  string `dynamodbav:"challenge"`
	BHash      string `dynamodbav:"b_hash"`
	CDigest    string `dynamodbav:"c_digest"`
	State      State  `dynamodbav:"state"`
	Generation int    `dynamodbav:"generation"`
	Version    int    `dynamodbav:"version"`
	Attempts   int    `dynamodbav:"attempts"`
	Resends    int    `dynamodbav:"resends"`
	Expires    int64  `dynamodbav:"expires"`
	LastSent   int64  `dynamodbav:"last_sent"`
	ClaimedAt  int64  `dynamodbav:"claimed_at"`
	TTL        int64  `dynamodbav:"ttl"`
	Subject    string `dynamodbav:"subject"`
}
type Store interface {
	Put(context.Context, Transaction) error
	Get(context.Context, string) (Transaction, error)
	Swap(context.Context, Transaction, Transaction) error
	Charge(context.Context, string, int, time.Time) error
}
type Sender interface {
	Send(context.Context, string, string, string) error
}
type Session struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int32  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}
type Identity interface {
	Eligible(context.Context, string) (bool, error)
	Confirm(context.Context, string) (string, error)
	Session(context.Context, string) (Session, error)
}
type Service struct {
	Store       Store
	Sender      Sender
	Identity    Identity
	Key         []byte
	Origin      string
	Now         func() time.Time
	Random      func([]byte) error
	Diagnostics bool
}

// OperationalError identifies the failed dependency without disclosing its message.
type OperationalError struct {
	Stage string
	Code  string
	Cause error
}

type traceKey struct{}

func WithTraceID(ctx context.Context, trace string) context.Context {
	return context.WithValue(ctx, traceKey{}, trace)
}

func event(ctx context.Context, message string) {
	trace, _ := ctx.Value(traceKey{}).(string)
	log.Printf("%s trace_id=%s", message, trace)
}

func (e *OperationalError) Error() string { return e.Stage + ": " + e.Code }

func (e *OperationalError) Unwrap() error { return e.Cause }

func operation(ctx context.Context, stage string, err error) *OperationalError {
	code := "provider_error"
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode()
		if stage == "ses_send" && strings.Contains(strings.ToLower(apiErr.ErrorMessage()), "not verified") {
			code = "SESIdentityNotVerified"
		}
	}
	// Provider messages can contain email addresses or proofs. Log only an AWS error code.
	for _, ch := range code {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			code = "provider_error"
			break
		}
	}
	if len(code) > 80 || code == "" {
		code = "provider_error"
	}
	trace, _ := ctx.Value(traceKey{}).(string)
	log.Printf("operation_failed trace_id=%s stage=%s provider_code=%s", trace, stage, code)
	return &OperationalError{Stage: stage, Code: code, Cause: err}
}

type IncorrectCode struct{ Remaining int }

func (e IncorrectCode) Error() string        { return ErrIncorrect.Error() }
func (e IncorrectCode) Is(target error) bool { return target == ErrIncorrect }

type SignupResult struct {
	RequestID string `json:"request_id"`
}
type ConfirmInput struct {
	RequestID string `json:"request_id"`
	TokenB    string `json:"token_b"`
	TokenA    string `json:"token_a,omitempty"`
	TokenC    string `json:"token_c,omitempty"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) random(b []byte) error {
	if s.Random != nil {
		return s.Random(b)
	}
	_, err := rand.Read(b)
	return err
}
func (s *Service) secret() (string, error) {
	b := make([]byte, 32)
	if err := s.random(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (s *Service) digest(parts ...string) string {
	mac := hmac.New(sha256.New, s.Key)
	for _, p := range parts {
		mac.Write([]byte(p))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}
func hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func validToken(v string) bool {
	b, e := base64.RawURLEncoding.DecodeString(v)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == v
}
func validChallenge(v string) bool { return validToken(v) }
func same(a, b string) bool        { return hmac.Equal([]byte(a), []byte(b)) }
func cleanEmail(email string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(e)
	if err != nil || parsed.Address != e || len(e) > 254 || strings.Count(e, "@") != 1 {
		return "", ErrInvalid
	}
	return e, nil
}
func (s *Service) code() (string, error) {
	var code strings.Builder
	for code.Len() < 6 {
		b := make([]byte, 1)
		if err := s.random(b); err != nil {
			return "", err
		}
		if b[0] < 250 {
			code.WriteByte('0' + b[0]%10)
		}
	}
	return code.String(), nil
}
func (s *Service) origin() (string, error) {
	u, e := url.Parse(s.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", ErrInvalid
	}
	return strings.TrimRight(s.Origin, "/"), nil
}

func (s *Service) Signup(ctx context.Context, email, challenge, method, source string) (SignupResult, error) {
	e, err := cleanEmail(email)
	if err != nil || method != "S256" || !validChallenge(challenge) {
		return SignupResult{}, ErrInvalid
	}
	origin, err := s.origin()
	if err != nil || len(s.Key) < 32 {
		return SignupResult{}, ErrInvalid
	}
	id, err := s.secret()
	if err != nil {
		return SignupResult{}, err
	}
	result := SignupResult{RequestID: id}
	if err = s.Store.Charge(ctx, "signup-source:"+s.digest(source), 10, s.now()); err != nil {
		if errors.Is(err, ErrLimited) {
			event(ctx, "signup_skipped reason=source_rate_limit")
			return result, nil
		}
		return result, operation(ctx, "signup_source_budget", err)
	}
	if err = s.Store.Charge(ctx, "signup-email:"+s.digest(e), 3, s.now()); err != nil {
		if errors.Is(err, ErrLimited) {
			event(ctx, "signup_skipped reason=email_rate_limit")
			return result, nil
		}
		return result, operation(ctx, "signup_email_budget", err)
	}
	eligible, err := s.Identity.Eligible(ctx, e)
	if err != nil {
		failure := operation(ctx, "signup_identity_lookup", err)
		if s.Diagnostics {
			return result, failure
		}
		return result, nil
	}
	if !eligible {
		event(ctx, "signup_skipped reason=account_exists")
		return result, nil
	}
	b, err := s.secret()
	if err != nil {
		return result, err
	}
	c, err := s.code()
	if err != nil {
		return result, err
	}
	now := s.now()
	t := Transaction{ID: id, Email: e, Challenge: challenge, BHash: hash(b), CDigest: s.digest(id, "1", c), State: Pending, Generation: 1, Version: 1, Expires: now.Add(10 * time.Minute).Unix(), TTL: now.Add(24 * time.Hour).Unix(), LastSent: now.Unix()}
	if err = s.Store.Put(ctx, t); err != nil {
		return result, operation(ctx, "signup_store_proof", err)
	}
	link := origin + "/verify-email?request_id=" + url.QueryEscape(id) + "&b=" + url.QueryEscape(b)
	// Preserve the generic public response outside diagnostic mode to avoid account enumeration.
	if err = s.Sender.Send(ctx, e, link, c); err != nil {
		failure := operation(ctx, "ses_send", err)
		if s.Diagnostics {
			return result, failure
		}
	} else {
		event(ctx, "signup_send_accepted")
	}
	return result, nil
}

func (s *Service) Resend(ctx context.Context, id, source string) error {
	if !validToken(id) {
		return ErrInvalid
	}
	if err := s.Store.Charge(ctx, "resend-source:"+s.digest(source), 10, s.now()); err != nil {
		if errors.Is(err, ErrLimited) {
			event(ctx, "resend_skipped reason=source_rate_limit")
			return nil
		}
		return operation(ctx, "resend_source_budget", err)
	}
	t, err := s.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			event(ctx, "resend_skipped reason=request_not_found")
			return nil
		}
		return operation(ctx, "resend_load_proof", err)
	}
	now := s.now()
	if (t.State != Pending && t.State != Failed) || t.Resends >= 3 || now.Unix()-t.LastSent < 60 {
		event(ctx, "resend_skipped reason=state_or_cooldown")
		return nil
	}
	if t.State == Failed {
		// A provider error before user creation invalidated the old proof.
		// Never resend a signup proof if a Cognito account now exists.
		eligible, lookupErr := s.Identity.Eligible(ctx, t.Email)
		if lookupErr != nil {
			failure := operation(ctx, "resend_identity_lookup", lookupErr)
			if s.Diagnostics {
				return failure
			}
			return nil
		}
		if !eligible {
			event(ctx, "resend_skipped reason=account_exists")
			return nil
		}
	}
	if err = s.Store.Charge(ctx, "resend-email:"+s.digest(t.Email), 3, now); err != nil {
		if errors.Is(err, ErrLimited) {
			event(ctx, "resend_skipped reason=email_rate_limit")
			return nil
		}
		return operation(ctx, "resend_email_budget", err)
	}
	b, err := s.secret()
	if err != nil {
		return err
	}
	c, err := s.code()
	if err != nil {
		return err
	}
	next := t
	next.State = Pending
	next.ClaimedAt = 0
	next.Generation++
	next.Version++
	next.Resends++
	next.Attempts = 0
	next.BHash = hash(b)
	next.CDigest = s.digest(id, fmt.Sprint(next.Generation), c)
	next.Expires = now.Add(10 * time.Minute).Unix()
	next.LastSent = now.Unix()
	if err = s.Store.Swap(ctx, t, next); err != nil {
		if errors.Is(err, ErrConflict) {
			event(ctx, "resend_skipped reason=concurrent_update")
			return nil
		}
		return operation(ctx, "resend_rotate_proof", err)
	}
	origin, err := s.origin()
	if err != nil {
		return err
	}
	if err = s.Sender.Send(ctx, t.Email, origin+"/verify-email?request_id="+url.QueryEscape(id)+"&b="+url.QueryEscape(b), c); err != nil {
		failure := operation(ctx, "ses_send", err)
		if s.Diagnostics {
			return failure
		}
	} else {
		event(ctx, "resend_send_accepted")
	}
	return nil
}

func (s *Service) Confirm(ctx context.Context, in ConfirmInput) (Session, error) {
	if !validToken(in.RequestID) || !validToken(in.TokenB) || (in.TokenA == "") == (in.TokenC == "") {
		return Session{}, ErrInvalid
	}
	t, err := s.Store.Get(ctx, in.RequestID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Session{}, ErrUnusable
		}
		return Session{}, operation(ctx, "confirm_load_proof", err)
	}
	if t.State == Confirmed {
		return Session{}, ErrUsed
	}
	if t.State == Confirming && s.now().Unix()-t.ClaimedAt >= 60 && t.Expires > s.now().Unix() && same(t.BHash, hash(in.TokenB)) {
		// The confirm Lambda timeout is 25 seconds. After 60 seconds an absent account
		// can safely reclaim an interrupted confirmation; an existing account
		// must recover via email OTP rather than receive another session here.
		eligible, lookupErr := s.Identity.Eligible(ctx, t.Email)
		if lookupErr != nil {
			failure := operation(ctx, "confirm_recovery_lookup", lookupErr)
			if s.Diagnostics {
				return Session{}, failure
			}
			return Session{}, ErrConflict
		}
		if !eligible {
			return Session{}, ErrSignInRequired
		}
		retry := t
		retry.State = Pending
		retry.Version++
		if err = s.Store.Swap(ctx, t, retry); err != nil {
			operation(ctx, "confirm_recover_proof", err)
			return Session{}, ErrConflict
		}
		t = retry
	}
	if t.State == Confirming {
		return Session{}, ErrConflict
	}
	if t.State != Pending || t.Expires <= s.now().Unix() || !same(t.BHash, hash(in.TokenB)) {
		return Session{}, ErrUnusable
	}
	if in.TokenA != "" {
		a, decodeErr := base64.RawURLEncoding.DecodeString(in.TokenA)
		if decodeErr != nil || len(a) != 32 {
			return Session{}, ErrUnusable
		}
		sum := sha256.Sum256(a)
		if !same(t.Challenge, base64.RawURLEncoding.EncodeToString(sum[:])) {
			return Session{}, ErrUnusable
		}
	} else {
		if len(in.TokenC) != 6 || strings.Trim(in.TokenC, "0123456789") != "" {
			return Session{}, ErrInvalid
		}
		if t.Attempts >= 5 {
			return Session{}, ErrLimited
		}
		if err = s.Store.Charge(ctx, "code-email:"+s.digest(t.Email), 10, s.now()); err != nil {
			if errors.Is(err, ErrLimited) {
				return Session{}, err
			}
			return Session{}, operation(ctx, "confirm_code_budget", err)
		}
		if !same(t.CDigest, s.digest(t.ID, fmt.Sprint(t.Generation), in.TokenC)) {
			next := t
			next.Attempts++
			next.Version++
			if err = s.Store.Swap(ctx, t, next); err != nil {
				operation(ctx, "confirm_code_attempt", err)
				return Session{}, ErrConflict
			}
			if next.Attempts >= 5 {
				return Session{}, ErrLimited
			}
			return Session{}, IncorrectCode{Remaining: 5 - next.Attempts}
		}
	}
	claim := t
	claim.State = Confirming
	claim.ClaimedAt = s.now().Unix()
	claim.Version++
	if err = s.Store.Swap(ctx, t, claim); err != nil {
		operation(ctx, "confirm_claim_proof", err)
		return Session{}, ErrConflict
	}
	sub, err := s.Identity.Confirm(ctx, t.Email)
	if err != nil {
		failure := operation(ctx, "cognito_create_user", err)
		failed := claim
		failed.State = Failed
		failed.Version++
		if swapErr := s.Store.Swap(ctx, claim, failed); swapErr != nil {
			operation(ctx, "confirm_mark_failed", swapErr)
		}
		eligible, lookupErr := s.Identity.Eligible(ctx, t.Email)
		if lookupErr != nil {
			operation(ctx, "confirm_failure_lookup", lookupErr)
		}
		if s.Diagnostics {
			return Session{}, failure
		}
		if lookupErr == nil && eligible {
			return Session{}, ErrUnusable
		}
		return Session{}, ErrSignInRequired
	}
	done := claim
	done.State = Confirmed
	done.Subject = sub
	done.BHash = ""
	done.CDigest = ""
	done.Challenge = ""
	done.Version++
	if err = s.Store.Swap(ctx, claim, done); err != nil {
		failure := operation(ctx, "confirm_store_confirmed", err)
		if s.Diagnostics {
			return Session{}, failure
		}
		return Session{}, ErrSignInRequired
	}
	session, err := s.Identity.Session(ctx, t.Email)
	if err != nil {
		failure := operation(ctx, "cognito_session", err)
		if s.Diagnostics {
			return Session{}, failure
		}
		return Session{}, ErrSignInRequired
	}
	event(ctx, "confirm_session_issued")
	return session, nil
}
