package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/build"
)

func TestReuseBuildRequiresAllArchivesAndSkipsCompile(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	o.ReuseBuild = true
	r := goodRunner()
	if execute(r, o) == nil {
		t.Fatal("accepted missing archives")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "pulumi up") {
			t.Fatal(call)
		}
	}
	if err := os.MkdirAll(filepath.Join(o.Root, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(o.Root, "bootstrap")
	if err := os.WriteFile(binary, []byte("test binary"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range build.LambdaNames {
		if err := build.PackageLambda(binary, filepath.Join(o.Root, "dist", name+".zip")); err != nil {
			t.Fatal(err)
		}
	}
	r = goodRunner()
	if err := execute(r, o); err != nil {
		t.Fatal(err)
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "go ") {
			t.Fatal("rebuilt", call)
		}
	}
	if !strings.HasPrefix(r.calls[len(r.calls)-1], "pulumi up") {
		t.Fatal(r.calls)
	}
}
