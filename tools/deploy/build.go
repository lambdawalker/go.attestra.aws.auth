package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

func packageLambda(binary, archive string) (err error) {
	in, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(archive), "lambda-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	z := zip.NewWriter(out)
	header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	header.SetMode(0755)
	entry, err := z.CreateHeader(header)
	if err == nil {
		_, err = io.Copy(entry, in)
	}
	if e := z.Close(); err == nil {
		err = e
	}
	if e := out.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	// Windows cannot rename over an existing destination.
	if err = os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(out.Name(), archive)
}

var lambdaNames = []string{"signup", "resend", "confirm", "challenge", "passkeyoptions", "passkeycomplete", "capture", "capture-worker", "capture-dispatcher", "auth-email-start", "auth-email-complete", "auth-passkey-start", "auth-passkey-complete", "auth-refresh", "auth-status"}

type lambdaBuild struct {
	source string
	names  []string
}

func lambdaBuilds() []lambdaBuild {
	jobs := []lambdaBuild{}
	indexes := map[string]int{}
	for _, name := range lambdaNames {
		source := "./cmd/" + name
		if len(name) > 5 && name[:5] == "auth-" {
			source = "./cmd/signin"
		}
		if i, ok := indexes[source]; ok {
			jobs[i].names = append(jobs[i].names, name)
		} else {
			indexes[source] = len(jobs)
			jobs = append(jobs, lambdaBuild{source: source, names: []string{name}})
		}
	}
	return jobs
}

// Wait for running jobs to exit before returning, even when a build fails.
func runLambdaBuilds(jobs []lambdaBuild, workers int, build func(context.Context, lambdaBuild) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := make(chan lambdaBuild, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for n := 0; n < max(1, min(workers, len(jobs))); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range queue {
				if ctx.Err() != nil {
					return
				}
				if err := build(ctx, job); err != nil {
					once.Do(func() { first = err; cancel() })
					return
				}
			}
		}()
	}
	wg.Wait()
	return first
}

func buildLambdas(root string) error {
	workers := min(4, max(1, runtime.NumCPU()/2))
	parallel := max(1, runtime.NumCPU()/workers)
	fmt.Printf("Building %d unique binaries for %d Lambdas with %d workers\n", len(lambdaBuilds()), len(lambdaNames), workers)
	return runLambdaBuilds(lambdaBuilds(), workers, func(ctx context.Context, job lambdaBuild) error {
		dir := filepath.Join(root, "dist", job.names[0])
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		binary := filepath.Join(dir, "bootstrap")
		cmd := exec.CommandContext(ctx, "go", "build", "-p", strconv.Itoa(parallel), "-trimpath", "-tags", "lambda.norpc", "-o", binary, job.source)
		cmd.Dir = root
		cmd.Env = append(withoutCredentials(os.Environ()), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		fmt.Printf("Building %s (%d Lambda archives)\n", job.source, len(job.names))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w\n%s", job.source, err, output.String())
		}
		for _, name := range job.names {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := packageLambda(binary, filepath.Join(root, "dist", name+".zip")); err != nil {
				return fmt.Errorf("package %s: %w", name, err)
			}
		}
		fmt.Printf("Built %s\n", job.source)
		return nil
	})
}

func checkLambdaArchives(root string) error {
	for _, name := range lambdaNames {
		path := filepath.Join(root, "dist", name+".zip")
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("built archive %s is missing or empty; restart deployment to rebuild", name)
		}
	}
	return nil
}
