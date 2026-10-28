// Package runtimeconfig owns the deliberately narrow public runtime contract.
package runtimeconfig

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/lambdawalker/go.attestra.aws.auth/capture"
)

var ErrInvalid = errors.New("invalid_config")
var ErrConflict = errors.New("revision_conflict")
var ErrForbidden = errors.New("environment_forbidden")
var ErrNotFound = errors.New("not_found")
var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func ValidEnvironment(v string) bool { return environmentPattern.MatchString(v) }

type Features struct {
	IDCapture bool `json:"idCapture" pulumi:"idCapture"`
}
type Capture struct {
	Messages     capture.Purposes `json:"messages" pulumi:"messages"`
	DocumentType string           `json:"documentType" pulumi:"documentType"`
	Jurisdiction string           `json:"jurisdiction" pulumi:"jurisdiction"`
	Purpose      string           `json:"purpose" pulumi:"purpose"`
}
type Config struct {
	SchemaVersion int         `json:"schemaVersion" pulumi:"schemaVersion"`
	Environment   string      `json:"environment" pulumi:"environment"`
	Revision      int64       `json:"revision" pulumi:"revision"`
	UpdatedAt     string      `json:"updatedAt" pulumi:"updatedAt"`
	Onboarding    *Onboarding `json:"onboarding,omitempty" pulumi:"onboarding"`
	Features      Features    `json:"features" pulumi:"features"`
	Capture       Capture     `json:"capture" pulumi:"capture"`
}

func Default(env string) Config {
	policy := DefaultOnboarding()
	return Config{Onboarding: &policy, Features: Features{IDCapture: true}, SchemaVersion: 1, Environment: env, UpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), Capture: Capture{DocumentType: "id_card", Purpose: capture.DefaultPurposes().EN, Messages: capture.DefaultPurposes()}}
}
func (c Config) Validate() error {
	if c.EffectiveOnboarding().Validate() != nil || (c.Onboarding != nil && c.Features.IDCapture != (c.Onboarding.IDCapture != Disabled)) {
		return ErrInvalid
	}
	if c.SchemaVersion != 1 || !ValidEnvironment(c.Environment) || c.Revision < 0 {
		return ErrInvalid
	}
	if _, e := time.Parse(time.RFC3339, c.UpdatedAt); e != nil {
		return ErrInvalid
	}
	for _, v := range []string{c.Capture.DocumentType, c.Capture.Purpose, c.Capture.Jurisdiction, c.Capture.Messages.EN, c.Capture.Messages.ES, c.Capture.Messages.FR} {
		if len(v) > 1024 || strings.TrimSpace(v) != v || strings.ContainsFunc(v, unicode.IsControl) {
			return ErrInvalid
		}
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(c.Capture.DocumentType) || (c.Features.IDCapture && (c.Capture.DocumentType == "sample_card" || c.Capture.Purpose == "")) {
		return ErrInvalid
	}
	if c.Capture.Messages != (capture.Purposes{}) && c.Capture.Messages.EN == "" {
		return ErrInvalid
	}
	return nil
}
func (c Config) Policy() capture.Policy {
	messages := c.Capture.Messages
	if messages.EN == "" {
		messages = capture.DefaultPurposes()
	}
	return capture.Policy{Enabled: c.Features.IDCapture, DocumentType: "id_card", Purpose: messages.EN, Purposes: messages, Slots: []string{"front", "back"}, ContentType: "image/jpeg", MaxBytes: capture.MaxBytes, RetentionDays: 7}.WithVersion()
}

type Version struct {
	Config         Config `json:"config"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	SourceRevision *int64 `json:"sourceRevision,omitempty"`
}
type Grant struct {
	Subject      string `json:"subject"`
	Environment  string `json:"environment"`
	Revision     int64  `json:"revision"`
	Active       bool   `json:"active"`
	ManageGrants bool   `json:"manageGrants"`
}
type Repository interface {
	Get(context.Context, string) (Config, error)
	Grants(context.Context, string) ([]Grant, error)
	History(context.Context, string) ([]Version, error)
	GetVersion(context.Context, string, int64) (Version, error)
	Publish(context.Context, Grant, int64, Version) error
	SetGrant(context.Context, Grant, Grant) error
}
type Service struct {
	Store Repository
	Now   func() time.Time
}

func (s Service) Authorized(ctx context.Context, sub, env string) (Grant, error) {
	gs, e := s.Store.Grants(ctx, sub)
	if e != nil {
		return Grant{}, e
	}
	for _, g := range gs {
		if g.Environment == env && g.Active {
			return g, nil
		}
	}
	return Grant{}, ErrForbidden
}
func (s Service) Publish(ctx context.Context, g Grant, expected int64, c Config, source *int64) (Config, error) {
	if expected < 0 || expected == 9223372036854775807 {
		return Config{}, ErrInvalid
	}
	if !g.Active || c.Environment != g.Environment {
		return Config{}, ErrForbidden
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	c.Capture.DocumentType = "id_card"
	c.Capture.Jurisdiction = ""
	if c.Capture.Messages == (capture.Purposes{}) {
		c.Capture.Messages = capture.DefaultPurposes()
	}
	c.Capture.Purpose = c.Capture.Messages.EN
	c = c.Normalized()
	c.Features.IDCapture = c.Onboarding.IDCapture != Disabled
	c.SchemaVersion = 1
	c.Revision = expected + 1
	c.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if e := c.Validate(); e != nil {
		return Config{}, e
	}
	action := "publish"
	if source != nil {
		action = "rollback"
	}
	e := s.Store.Publish(ctx, g, expected, Version{Config: c, Actor: g.Subject, Action: action, SourceRevision: source})
	return c, e
}
