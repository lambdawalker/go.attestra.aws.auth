package build

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveHasLinuxExecutableBootstrap(t *testing.T) {
	dir := t.TempDir()
	binary, archive := filepath.Join(dir, "bootstrap"), filepath.Join(dir, "capture.zip")
	if err := os.WriteFile(binary, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PackageLambda(binary, archive); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "bootstrap" || z.File[0].Mode().Perm() != 0755 {
		t.Fatal("archive lacks executable bootstrap")
	}
}
