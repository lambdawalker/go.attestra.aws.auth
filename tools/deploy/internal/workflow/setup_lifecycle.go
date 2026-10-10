package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

// A new setup explicitly starts a new lifecycle. Archive completed receipts
// before any provider mutation so even a partially recreated environment can
// be torn down. Never reuse old resource IDs or bypass unfinished cleanup.
func beginSetupLifecycle(root, repository, environment string) (func(), error) {
	if err := ui.ValidateEnvironment(environment); err != nil {
		return nil, err
	}
	path := filepath.Join(root, ".attestra", "teardown."+environment+".local.json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("setup/teardown is locked for %s; verify no process is running before removing %s: %w", environment, path+".lock", err)
	}
	fmt.Fprintf(lock, "setup pid %d\n", os.Getpid())
	lock.Close()
	var once sync.Once
	release := func() { once.Do(func() { _ = os.Remove(path + ".lock") }) }
	fail := func(err error) (func(), error) { release(); return nil, err }
	for _, suffix := range []string{".bak", ".tmp"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			return fail(fmt.Errorf("inspect interrupted teardown checkpoint %s before setup", path+suffix))
		}
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return release, nil
	}
	if err != nil {
		return fail(err)
	}
	var p teardownProgress
	if json.Unmarshal(data, &p) != nil || p.Repository != repository || p.Environment != environment {
		return fail(fmt.Errorf("teardown checkpoint does not match repository/environment: %s", path))
	}
	if !p.Complete {
		return fail(fmt.Errorf("unfinished teardown for %s; resume teardown before setting up this environment again: %s", environment, path))
	}
	history := filepath.Join(root, ".attestra", "teardown-history")
	if err := os.MkdirAll(history, 0700); err != nil {
		return fail(err)
	}
	archive, err := os.MkdirTemp(history, environment+"-")
	if err != nil {
		return fail(err)
	}
	// Only backups made by this completed teardown are retired. Other YAML and
	// migration backups retain bootstrapStack's existing state-loss protection.
	backups, err := filepath.Glob(filepath.Join(root, "infra", "Pulumi."+environment+".yaml.bak.teardown.*"))
	if err != nil {
		return fail(err)
	}
	for _, backup := range backups {
		if err := os.Rename(backup, filepath.Join(archive, filepath.Base(backup))); err != nil {
			return fail(err)
		}
	}
	// Move the completion guard last; earlier failures still block recreation.
	if err := os.Rename(path, filepath.Join(archive, filepath.Base(path))); err != nil {
		return fail(err)
	}
	fmt.Printf("Starting a new setup lifecycle for %s. Previous completed teardown and its configuration backups archived at %s.\n", environment, archive)
	return release, nil
}
