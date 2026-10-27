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
	IDCapture bool `json:"idCapture"`
}
type Capture struct {
	DocumentType string `json:"documentType"`
	Jurisdiction string `json:"jurisdiction"`
	Purpose      string `json:"purpose"`
}
type Config struct {
	SchemaVersion int      `json:"schemaVersion"`
	Environment   string   `json:"environment"`
	Revision      int64    `json:"revision"`
	UpdatedAt     string   `json:"updatedAt"`
	Features      Features `json:"features"`
	Capture       Capture  `json:"capture"`
}

func Default(env string) Config {
	return Config{SchemaVersion: 1, Environment: env, UpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), Capture: Capture{DocumentType: "sample_card"}}
}
func (c Config) Validate() error {
	if c.SchemaVersion != 1 || !ValidEnvironment(c.Environment) || c.Revision < 0 {
		return ErrInvalid
	}
	if _, e := time.Parse(time.RFC3339, c.UpdatedAt); e != nil {
		return ErrInvalid
	}
	for _, v := range []string{c.Capture.DocumentType, c.Capture.Purpose, c.Capture.Jurisdiction} {
		if len(v) > 1024 || strings.TrimSpace(v) != v || strings.ContainsFunc(v, unicode.IsControl) {
			return ErrInvalid
		}
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(c.Capture.DocumentType) || (c.Features.IDCapture && (c.Capture.DocumentType == "sample_card" || c.Capture.Purpose == "" || c.Capture.Jurisdiction == "")) {
		return ErrInvalid
	}
	return nil
}
func (c Config) Policy() capture.Policy {
	return capture.Policy{Enabled: c.Features.IDCapture, DocumentType: c.Capture.DocumentType, Jurisdiction: c.Capture.Jurisdiction, Purpose: c.Capture.Purpose, Slots: []string{"front", "back"}, ContentType: "image/jpeg", MaxBytes: capture.MaxBytes, RetentionDays: 7}.WithVersion()
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
	if !g.Active || c.Environment != g.Environment {
		return Config{}, ErrForbidden
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
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
