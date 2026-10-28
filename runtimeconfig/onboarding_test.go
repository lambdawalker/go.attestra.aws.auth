package runtimeconfig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOnboardingDefaultsAndLegacy(t *testing.T) {
	c := Default("dev")
	if !c.Features.IDCapture || c.EffectiveOnboarding().IDCapture != Required || c.EffectiveOnboarding().Passkey != Required || c.EffectiveOnboarding().Password != Disabled || c.EffectiveOnboarding().PasswordPlacement != "hidden" {
		t.Fatal(c)
	}
	for _, enabled := range []bool{false, true} {
		old := Config{Features: Features{IDCapture: enabled}}
		p := old.EffectiveOnboarding()
		expected := Disabled
		if enabled {
			expected = Optional
		}
		if p.IDCapture != expected || p.Passkey != Required || p.Password != Disabled {
			t.Fatal(p)
		}
	}
	var invalid Config
	if err := json.Unmarshal([]byte(`{"onboarding":{}}`), &invalid); err != nil {
		t.Fatal(err)
	}
	if invalid.EffectiveOnboarding().Validate() == nil {
		t.Fatal("empty object treated as legacy")
	}
}
func TestOnboardingValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Onboarding){
		"bad mode":         func(p *Onboarding) { p.Passkey = "needed" },
		"hidden required":  func(p *Onboarding) { p.Password = Required },
		"disabled visible": func(p *Onboarding) { p.PasswordPlacement = "with_email" },
		"small minimum":    func(p *Onboarding) { p.PasswordPolicy.MinLength = 7 },
		"large maximum":    func(p *Onboarding) { p.PasswordPolicy.MaxLength = 129 },
		"inverted":         func(p *Onboarding) { p.PasswordPolicy.MinLength = 100; p.PasswordPolicy.MaxLength = 99 },
		"no classes": func(p *Onboarding) {
			p.PasswordPolicy.Lowercase = Disallowed
			p.PasswordPolicy.Uppercase = Disallowed
			p.PasswordPolicy.Numbers = Disallowed
			p.PasswordPolicy.Symbols = Disallowed
		},
		"unknown class": func(p *Onboarding) { p.PasswordPolicy.Numbers = "yes" },
	} {
		t.Run(name, func(t *testing.T) {
			p := DefaultOnboarding()
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("accepted invalid policy")
			}
		})
	}
}
func TestPasswordRules(t *testing.T) {
	p := DefaultOnboarding().PasswordPolicy
	p.MinLength = 8
	p.Lowercase = Disallowed
	p.Uppercase = CharacterRequired
	p.Numbers = CharacterRequired
	p.Symbols = Disallowed
	for _, v := range []string{"ABCDEFG1", "1ABCDEFG"} {
		if p.Check(v) != nil {
			t.Fatalf("rejected valid %q", v)
		}
	}
	for _, v := range []string{"AbCDEFG1", "ABCDEFG!", "ABCDEFGH", "12345678", "ABC1", "ABCDEFG1 ", "ABCDEFé1", strings.Repeat("A", 129) + "1"} {
		if p.Check(v) == nil {
			t.Fatalf("accepted invalid %q", v)
		}
	}
}

func TestExplicitNullOnboardingIsNotLegacy(t *testing.T) {
	var c Config
	if json.Unmarshal([]byte(`{"onboarding":null}`), &c) == nil {
		t.Fatal("explicit null accepted as omitted legacy policy")
	}
}
func TestPublishPersistsFullPolicyAndLegacyRollback(t *testing.T) {
	m := &memoryRepository{config: Default("dev"), grant: Grant{Subject: "admin", Environment: "dev", Revision: 1, Active: true}}
	s := Service{Store: m}
	c := Default("dev")
	c.Onboarding.IDCapture = Disabled
	c.Onboarding.Password = Required
	c.Onboarding.PasswordPlacement = "with_email"
	got, e := s.Publish(context.Background(), m.grant, 0, c, nil)
	if e != nil || got.Features.IDCapture || got.Onboarding.Password != Required {
		t.Fatal(got, e)
	}
	legacy := Default("dev")
	legacy.Onboarding = nil
	legacy.Features.IDCapture = true
	got, e = s.Publish(context.Background(), m.grant, 1, legacy, nil)
	if e != nil || got.Onboarding.IDCapture != Optional || got.Onboarding.Password != Disabled {
		t.Fatal(got, e)
	}
}
