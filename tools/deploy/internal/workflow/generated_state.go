package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Called once at CLI startup, before state discovery. Move legacy receipts
// intact, including recovery siblings; never merge or overwrite two histories.
func prepareGeneratedState(root string) error {
	dir := filepath.Join(root, ".attestra")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, "migration.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("local state migration is locked at %s: %w", lockPath, err)
	}
	lock.Close()
	defer os.Remove(lockPath)
	var paths []string
	for _, pattern := range []string{"bootstrap.local.json*", "bootstrap.*.local.json*", "teardown.*.local.json*", "index-publication.*.local.json*", "teardown-history"} {
		found, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return err
		}
		paths = append(paths, found...)
	}
	if len(paths) == 0 {
		return nil
	}
	active, err := filepath.Glob(filepath.Join(dir, "*.lock"))
	if err != nil {
		return err
	}
	for _, path := range active {
		if path != lockPath {
			return fmt.Errorf("stop the active setup/deployment/teardown before migrating local state: %s", path)
		}
	}
	for _, path := range paths {
		if strings.HasSuffix(path, ".lock") {
			return fmt.Errorf("stop the active setup/teardown before migrating local state; if it crashed, inspect its lock first: %s", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !(info.IsDir() && filepath.Base(path) == "teardown-history") {
			return fmt.Errorf("unsupported generated state entry: %s", path)
		}
		target := filepath.Join(dir, filepath.Base(path))
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			return fmt.Errorf("both old and new state paths may exist; reconcile without discarding progress: %s and %s", path, target)
		}
	}
	for _, path := range paths {
		if err := os.Rename(path, filepath.Join(dir, filepath.Base(path))); err != nil {
			return fmt.Errorf("local state migration stopped; moved files retained, rerun to resume: %w", err)
		}
	}
	fmt.Println("Moved wizard progress and recovery files into .attestra/.")
	return nil
}
