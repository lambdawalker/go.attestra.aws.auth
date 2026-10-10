// Command deploy runs the Attestra environment tools.
package main

import (
	"fmt"
	"os"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/workflow"
)

func main() {
	if err := workflow.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Deployment stopped:", err)
		os.Exit(1)
	}
}
