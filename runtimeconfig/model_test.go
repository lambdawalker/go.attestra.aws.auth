package runtimeconfig

import (
	"context"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	c := Default("dev")
	c.Features.IDCapture = true
	c.Capture = Capture{DocumentType: "passport", Purpose: "Verify account ownership", Jurisdiction: "EU"}
	return c
}
func TestPublicValidationAndPolicyIdentity(t *testing.T) {
	base := validConfig()
	if e := base.Validate(); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*Config){"schema": func(c *Config) { c.SchemaVersion = 2 }, "environment": func(c *Config) { c.Environment = strings.Repeat("a", 33) }, "revision": func(c *Config) { c.Revision = -1 }, "date": func(c *Config) { c.UpdatedAt = "today" }, "sample": func(c *Config) { c.Capture.DocumentType = "sample_card" }, "kind": func(c *Config) { c.Capture.DocumentType = "passport/../secret" }, "long kind": func(c *Config) { c.Capture.DocumentType = strings.Repeat("a", 65) }, "purpose": func(c *Config) { c.Capture.Purpose = "" }, "control": func(c *Config) { c.Capture.Purpose = "foo\nbar" }, "bytes": func(c *Config) { c.Capture.Jurisdiction = strings.Repeat("é", 513) }} {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			if c.Validate() != ErrInvalid {
				t.Fatal("accepted invalid public document")
			}
		})
	}
	p := base.Policy()
	next := base
	next.Revision = 42
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if p.Version != next.Policy().Version {
		t.Fatal("metadata changed policy identity")
	}
	next.Capture.Messages = capture.DefaultPurposes()
	next.Capture.Messages.EN = "A changed disclosure"
	if p.Version == next.Policy().Version {
		t.Fatal("content change retained consent identity")
	}
	disabled := Default("dev")
	if disabled.Validate() != nil || disabled.Features.IDCapture {
		t.Fatal("unsafe default")
	}
}

type memoryRepository struct {
	config   Config
	grant    Grant
	versions []Version
}

func (m *memoryRepository) Get(context.Context, string) (Config, error) { return m.config, nil }
func (m *memoryRepository) Grants(context.Context, string) ([]Grant, error) {
	return []Grant{m.grant}, nil
}
func (m *memoryRepository) GetVersion(_ context.Context, _ string, rev int64) (Version, error) {
	for _, v := range m.versions {
		if v.Config.Revision == rev {
			return v, nil
		}
	}
	return Version{}, ErrNotFound
}
func (m *memoryRepository) History(context.Context, string) ([]Version, error) {
	return m.versions, nil
}
func (m *memoryRepository) Publish(_ context.Context, g Grant, expected int64, v Version) error {
	if !m.grant.Active || m.grant.Revision != g.Revision || m.config.Revision != expected {
		return ErrConflict
	}
	m.config = v.Config
	m.versions = append(m.versions, v)
	return nil
}
func (m *memoryRepository) SetGrant(context.Context, Grant, Grant) error { return nil }
func TestPublishConcurrencyRollbackAndRevocation(t *testing.T) {
	ctx := context.Background()
	m := &memoryRepository{config: validConfig(), grant: Grant{Subject: "admin", Environment: "dev", Revision: 1, Active: true}}
	s := Service{Store: m, Now: func() time.Time { return time.Unix(123, 0) }}
	g, e := s.Authorized(ctx, "admin", "dev")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authorized(ctx, "admin", "prod"); e != ErrForbidden {
		t.Fatal("cross-environment authorization")
	}
	original := m.config
	c := original
	c.Capture.Purpose = "Changed"
	published, e := s.Publish(ctx, g, 0, c, nil)
	if e != nil || published.Revision != 1 {
		t.Fatal(published, e)
	}
	if _, e = s.Publish(ctx, g, 0, original, nil); e != ErrConflict {
		t.Fatal("stale revision accepted")
	}
	source := int64(0)
	rollback, e := s.Publish(ctx, g, 1, original, &source)
	if e != nil || rollback.Revision != 2 || m.versions[1].Action != "rollback" || m.versions[1].SourceRevision == nil {
		t.Fatal("rollback did not append revision")
	}
	m.grant.Active = false
	m.grant.Revision++
	if _, e = s.Publish(ctx, g, 2, c, nil); e != ErrConflict {
		t.Fatal("revoked grant was reusable")
	}
}

func TestGenericLocalizedDisclosure(t *testing.T) {
	c := Default("dev")
	c.Features.IDCapture = true
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	p := c.Policy()
	if p.DocumentType != "id_card" || p.Jurisdiction != "" || p.Purposes.ES == "" {
		t.Fatal("missing generic policy")
	}
	c.Capture.Messages.ES = "Nuevo texto"
	if p.Version == c.Policy().Version {
		t.Fatal("translation must change consent version")
	}
	c.Capture.Messages.EN = ""
	if c.Validate() != ErrInvalid {
		t.Fatal("English fallback required")
	}
}
