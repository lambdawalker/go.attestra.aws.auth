package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/github"
)

func (n *nukeWorkflow) cleanupEnvironments(callerARN string) {
	a := n.api("us-east-2")
	// Setup roles live outside Pulumi. Discover by immutable ownership tags,
	// so roles survive in scope even after their GitHub environment is missing.
	var roles struct {
		Roles *[]struct{ RoleName, Arn string }
	}
	if _, err := a.Call(&roles, "iam", "list-roles"); err != nil {
		n.failure("Deployment role discovery", err)
	} else if roles.Roles == nil {
		n.failure("Deployment role discovery", errors.New("incomplete IAM role listing"))
	} else {
		for _, role := range *roles.Roles {
			var snapshot struct{ Role awsRoleSnapshot }
			found, err := a.Call(&snapshot, "iam", "get-role", "--role-name", role.RoleName)
			if err != nil {
				n.failure("Inspect role "+role.RoleName, err)
				continue
			}
			if !found {
				continue
			}
			environment, subject := "", ""
			for _, tag := range snapshot.Role.Tags {
				if tag.Key == "attestra-setup" && strings.HasPrefix(tag.Value, "github-") {
					environment = strings.TrimPrefix(tag.Value, "github-")
				}
				if tag.Key == "attestra-subject" {
					subject = tag.Value
				}
			}
			legacy := "repo:" + n.receipt.Repository + ":environment:" + environment
			immutable := fmt.Sprintf("repo:%s@%d/%s@%d:environment:%s", n.metadata.Owner.Login, n.metadata.Owner.ID, n.metadata.Name, n.metadata.ID, environment)
			if environment == "" || (subject != legacy && subject != immutable) {
				continue
			}
			if strings.Contains(callerARN, ":assumed-role/"+role.RoleName+"/") {
				n.failure("Deployment role "+role.Arn, errors.New("cannot delete the administrator session's own role"))
				continue
			}
			n.record(nukeStep("Deployment role "+role.Arn, func() (bool, error) { return a.Call(nil, "iam", "get-role", "--role-name", role.RoleName) }, func() error { return deleteTeardownRole(a, role.Arn, environment, n.metadata) }))
		}
	}
	environments := slices.Clone(n.receipt.Environments)
	for _, t := range n.receipt.Targets {
		if t.Project != "attestra-auth-email" {
			continue
		}
		environment := t.Evidence.Environment
		if environment == "" {
			environment = t.Stack
		}
		if !slices.Contains(environments, environment) {
			environments = append(environments, environment)
		}
		if arn := t.Evidence.Values["AWS_ROLE_ARN"]; arn != "" {
			parts := strings.Split(arn, ":role/")
			if len(parts) != 2 {
				n.failure("Deployment role", errors.New("invalid role ARN"))
				continue
			}
			if strings.Contains(callerARN, ":assumed-role/"+parts[1]+"/") {
				n.failure("Deployment role "+arn, errors.New("cannot delete the administrator session's own role"))
				continue
			}
			n.record(nukeStep("Deployment role "+arn, func() (bool, error) { return a.Call(nil, "iam", "get-role", "--role-name", parts[1]) }, func() error { return deleteTeardownRole(a, arn, environment, n.metadata) }))
		}
	}
	for _, environment := range environments {
		path := github.EnvironmentPath(n.receipt.Repository, environment)
		n.record(nukeStep("GitHub environment "+environment, func() (bool, error) {
			snapshot, err := n.g.InspectEnvironment(n.receipt.Repository, environment)
			if err != nil {
				return false, err
			}
			if account := snapshot.Variables["AWS_ACCOUNT_ID"]; account != "" && account != n.receipt.Account {
				return false, errors.New("GitHub environment now points to another account; retained")
			}
			return snapshot.Exists, nil
		}, func() error {
			if err := ensureNoNukeDeployments(n.g, n.receipt.Repository); err != nil {
				return err
			}
			_, err := n.g.Request("DELETE", path, nil, nil)
			return err
		}))
	}
	// Preserve local recovery settings if AWS/DNS/GitHub cleanup was incomplete.
	if !nukeSuccessful(n.receipt.Results) {
		return
	}
	for _, environment := range environments {
		p := teardownProgress{Repository: n.receipt.Repository, Environment: environment}
		if err := cleanupTeardownLocal(n.root, &p); err != nil {
			n.failure("Local setup "+environment, err)
			continue
		}
		path := filepath.Join(n.root, "infra", "Pulumi."+environment+".yaml")
		n.record(nukeStep("Local stack config "+environment, func() (bool, error) {
			_, err := os.Lstat(path)
			if os.IsNotExist(err) {
				return false, nil
			}
			return err == nil, err
		}, func() error { return os.Remove(path) }))
	}
	for _, t := range n.receipt.Targets {
		if t.Project != "attestra-index" {
			continue
		}
		path := filepath.Join(n.root, "infra-index", "Pulumi."+t.Stack+".yaml")
		n.record(nukeStep("Local shared stack config "+t.Stack, func() (bool, error) {
			_, err := os.Lstat(path)
			if os.IsNotExist(err) {
				return false, nil
			}
			return err == nil, err
		}, func() error { return os.Remove(path) }))
	}
}
