package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func stateTestDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".attestra"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMigrateGeneratedState(t *testing.T) {
	root := t.TempDir()
	names := []string{"bootstrap.dev.local.json", "teardown.qa.local.json.bak", "index-publication.dev.local.json"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareGeneratedState(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, ".attestra", name))
		if err != nil || string(data) != name {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("legacy file remains")
		}
	}
	if err := prepareGeneratedState(root); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRefusesConflictsAndActiveLocks(t *testing.T) {
	for _, conflict := range []string{"destination", "lock"} {
		root := stateTestDir(t)
		name := "bootstrap.dev.local.json"
		if err := os.WriteFile(filepath.Join(root, name), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(root, ".attestra", name)
		if conflict == "lock" {
			other = filepath.Join(root, "teardown.dev.local.json.lock")
		}
		if err := os.WriteFile(other, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := prepareGeneratedState(root); err == nil {
			t.Fatal("unsafe migration accepted")
		}
		data, _ := os.ReadFile(filepath.Join(root, name))
		if string(data) != "original" {
			t.Fatal("source changed")
		}
	}
}

func TestMigrationPreservesTeardownHistoryAndRecoverySiblings(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "teardown-history", "dev-old")
	if err := os.MkdirAll(history, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(history, "receipt.json"), []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", ".bak", ".tmp"} {
		if err := os.WriteFile(filepath.Join(root, "bootstrap.dev.local.json"+suffix), []byte(suffix), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareGeneratedState(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, ".attestra", "teardown-history", "dev-old", "receipt.json")); err != nil || string(data) != "history" {
		t.Fatal("history lost")
	}
	for _, suffix := range []string{"", ".bak", ".tmp"} {
		if data, err := os.ReadFile(filepath.Join(root, ".attestra", "bootstrap.dev.local.json"+suffix)); err != nil || string(data) != suffix {
			t.Fatal("recovery data lost")
		}
	}
}
