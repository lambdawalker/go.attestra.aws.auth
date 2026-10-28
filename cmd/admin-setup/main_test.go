package main

import "testing"

func TestAuthenticationOnlyNeedsNoEnvironmentOrInvite(t *testing.T) {
	if err := validateSetupArgs(true, "pool", "https://login.example.com", "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		auth                           bool
		pool, login, table, email, env string
	}{
		{true, "", "https://login.example.com", "", "", ""},
		{true, "pool", "", "", "", ""},
		{true, "pool", "https://login.example.com", "table", "admin@example.com", "dev"},
		{false, "pool", "https://login.example.com", "", "", ""},
	} {
		if validateSetupArgs(v.auth, v.pool, v.login, v.table, v.email, v.env) == nil {
			t.Fatal("accepted invalid arguments")
		}
	}
	if err := validateSetupArgs(false, "pool", "https://login.example.com", "table", "admin@example.com", "dev"); err != nil {
		t.Fatal(err)
	}
}
