package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupArchivesCompletedTeardownForSelectedEnvironment(t *testing.T) {
	root := stateTestDir(t)
	for _, env := range []string{"dev", "qa"} {
		p := teardownProgress{Repository: "owner/repo", Environment: env, Complete: true}
		if err := p.save(filepath.Join(root, ".attestra", "teardown."+env+".local.json")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "infra"), 0700); err != nil {
		t.Fatal(err)
	}
	migration := filepath.Join(root, "infra", "Pulumi.dev.yaml.bak.migration")
	if err := os.WriteFile(migration, []byte("preserved migration backup"), 0600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "infra", "Pulumi.dev.yaml.bak.teardown.old")
	if err := os.WriteFile(backup, []byte("encrypted backup"), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := beginSetupLifecycle(root, "owner/repo", "dev")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if data, err := os.ReadFile(migration); err != nil || string(data) != "preserved migration backup" {
		t.Fatal("unrelated migration backup changed")
	}

	if _, err := os.Stat(filepath.Join(root, ".attestra", "teardown.dev.local.json")); !os.IsNotExist(err) {
		t.Fatal("old dev receipt remains active")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("teardown backup still blocks new stack")
	}
	if complete, err := completedTeardown(root, "qa"); err != nil || !complete {
		t.Fatal("QA receipt changed")
	}
	archives, _ := filepath.Glob(filepath.Join(root, ".attestra", "teardown-history", "dev-*", "teardown.dev.local.json"))
	if len(archives) != 1 {
		t.Fatalf("archives: %v", archives)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(archives[0]), filepath.Base(backup))); err != nil {
		t.Fatal(err)
	}
	if _, err := beginSetupLifecycle(root, "owner/repo", "dev"); err == nil {
		t.Fatal("concurrent setup allowed")
	}
	release()
	again, err := beginSetupLifecycle(root, "owner/repo", "dev")
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestSetupPreservesIncompleteOrForeignTeardown(t *testing.T) {
	for _, p := range []teardownProgress{
		{Repository: "owner/repo", Environment: "dev"},
		{Repository: "other/repo", Environment: "dev", Complete: true},
		{Repository: "owner/repo", Environment: "qa", Complete: true},
	} {
		root := stateTestDir(t)
		path := filepath.Join(root, ".attestra", "teardown.dev.local.json")
		if err := p.save(path); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		if _, err := beginSetupLifecycle(root, "owner/repo", "dev"); err == nil {
			t.Fatal("unsafe receipt accepted")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("receipt modified")
		}
		if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
			t.Fatal("failed setup left lock")
		}
	}
}

func TestSetupPreservesInterruptedTeardown(t *testing.T) {
	for _, suffix := range []string{".bak", ".tmp"} {
		root := stateTestDir(t)
		path := filepath.Join(root, ".attestra", "teardown.dev.local.json")
		p := teardownProgress{Repository: "owner/repo", Environment: "dev", Complete: true}
		if err := p.save(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+suffix, []byte("recovery"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := beginSetupLifecycle(root, "owner/repo", "dev"); err == nil {
			t.Fatal("ignored interrupted write")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("original receipt lost")
		}
	}
}
