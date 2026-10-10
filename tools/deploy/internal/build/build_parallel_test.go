package build

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLambdaBuildsDeduplicatesSignin(t *testing.T) {
	jobs := lambdaBuilds()
	seen := map[string]bool{}
	for _, job := range jobs {
		if job.source == "./cmd/signin" && len(job.names) != 6 {
			t.Fatal(job)
		}
		for _, name := range job.names {
			if seen[name] {
				t.Fatal("duplicate", name)
			}
			seen[name] = true
		}
	}
	if len(jobs) != 10 || len(seen) != 15 {
		t.Fatal(len(jobs), len(seen))
	}
}

func TestLambdaBuildsBoundedAndCancels(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)
	var active, peak atomic.Int32
	failure := errors.New("compile failed")
	go func() {
		done <- runLambdaBuilds(lambdaBuilds(), 2, func(ctx context.Context, job lambdaBuild) error {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			started <- struct{}{}
			if job.source == "./cmd/signup" {
				<-release
				return failure
			}
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	for n := 0; n < 2; n++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("builds did not start concurrently")
		}
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers failed to cancel")
	}
	if peak.Load() != 2 || active.Load() != 0 {
		t.Fatal(peak.Load(), active.Load())
	}
}
