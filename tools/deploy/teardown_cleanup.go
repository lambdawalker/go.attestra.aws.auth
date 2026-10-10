package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type teardownStep struct {
	Name string
	Run  func() error
}

func runTeardownSteps(steps []teardownStep) error {
	for _, step := range steps {
		if err := step.Run(); err != nil {
			return fmt.Errorf("%s stopped: %w; completed removals are permanent; rerun teardown to resume", step.Name, err)
		}
	}
	return nil
}
func ensureDestroyed(data []byte) error {
	var state struct {
		Deployment *struct{ Resources []struct{ Type string } }
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment == nil {
		return errors.New("cannot verify empty Pulumi state")
	}
	for _, r := range state.Deployment.Resources {
		if r.Type != "pulumi:pulumi:Stack" && !strings.HasPrefix(r.Type, "pulumi:providers:") {
			return fmt.Errorf("resource remains in state: %s; external cleanup was not started", r.Type)
		}
	}
	return nil
}
func dnsDeletionCandidates(want dnsRecord, existing []dnsRecord) ([]dnsRecord, error) {
	var matches []dnsRecord
	for _, r := range existing {
		if dnsName(r.Name) != dnsName(want.Name) {
			return nil, errors.New("unexpected DNS name")
		}
		same := r.Type == want.Type && ((r.Type == "TXT" && strings.Trim(r.Content, `"`) == strings.Trim(want.Content, `"`)) || (r.Type == "CNAME" && dnsName(r.Content) == dnsName(want.Content)))
		if same {
			if !validCloudflareID(r.ID) {
				return nil, errors.New("invalid DNS record ID")
			}
			matches = append(matches, r)
		} else if want.Type == "CNAME" || r.Type == "CNAME" || r.Type == "NS" {
			return nil, fmt.Errorf("DNS conflict at %s; inspect it before teardown", want.Name)
		}
	}
	return matches, nil
}
func (c *cloudflareClient) deleteDNS(zone string, records []dnsRecord) error {
	for _, approved := range records {
		current, err := c.records(zone, approved.Name)
		if err != nil {
			return err
		}
		matches, err := dnsDeletionCandidates(approved, current)
		if err != nil {
			return err
		}
		found := false
		for _, r := range matches {
			if r.ID == approved.ID {
				found = true
			} else {
				return errors.New("matching DNS record was replaced since approval; inspect the saved teardown plan")
			}
		}
		if !found {
			continue
		}
		if _, err = c.request("DELETE", "/zones/"+zone+"/dns_records/"+approved.ID, nil, nil); err != nil {
			return err
		}
		fmt.Println("✓ Deleted DNS record", approved.Type, approved.Name)
	}
	for _, approved := range records {
		current, err := c.records(zone, approved.Name)
		if err != nil {
			return err
		}
		for _, r := range current {
			if r.ID == approved.ID {
				return errors.New("DNS deletion read-back failed")
			}
		}
	}
	return nil
}
func (g *githubClient) ensureNoDeployments(repo string) error {
	for _, status := range []string{"queued", "in_progress", "waiting", "pending", "requested"} {
		var result struct {
			TotalCount int `json:"total_count"`
		}
		found, err := g.request("GET", "/repos/"+repo+"/actions/workflows/deploy.yml/runs?per_page=1&status="+status, nil, &result)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("cannot inspect Deploy workflow; no teardown authorized")
		}
		if result.TotalCount > 0 {
			return errors.New("Deploy workflow has active or waiting runs; finish/cancel them before teardown")
		}
	}
	return nil
}
func (g *githubClient) checkTeardownSharing(repo, environment string, values map[string]string) error {
	names, err := g.listEnvironments(repo)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == environment {
			continue
		}
		other, err := g.inspectEnvironment(repo, name)
		if err != nil {
			return err
		}
		if other.Variables["AWS_ROLE_ARN"] == values["AWS_ROLE_ARN"] || (other.Variables["PULUMI_BACKEND_URL"] == values["PULUMI_BACKEND_URL"] && other.Variables["PULUMI_STACK"] == values["PULUMI_STACK"]) {
			return fmt.Errorf("environment %s shares this role or stack; resolve sharing before teardown", name)
		}
	}
	return nil
}
func inspectTeardownRole(a awsSetupAPI, arn, environment string, metadata repositoryMetadata) (string, []string, error) {
	parts := strings.Split(arn, ":role/")
	if len(parts) != 2 {
		return "", nil, errors.New("invalid role ARN")
	}
	name := parts[1]
	if strings.Contains(name, "/") {
		return "", nil, errors.New("role paths require manual cleanup")
	}
	var result struct{ Role awsRoleSnapshot }
	found, err := a.call(&result, "iam", "get-role", "--role-name", name)
	if err != nil {
		return "", nil, err
	}
	if !found {
		return name, nil, nil
	}
	legacy := "repo:" + metadata.Owner.Login + "/" + metadata.Name + ":environment:" + environment
	immutable := fmt.Sprintf("repo:%s@%d/%s@%d:environment:%s", metadata.Owner.Login, metadata.Owner.ID, metadata.Name, metadata.ID, environment)
	owned, bound := false, false
	subject := ""
	for _, t := range result.Role.Tags {
		if t.Key == "attestra-setup" && t.Value == "github-"+environment {
			owned = true
		}
		if t.Key == "attestra-subject" && (t.Value == legacy || t.Value == immutable) {
			bound = true
			subject = t.Value
		}
	}
	if result.Role.Arn != arn || !owned || !bound {
		return "", nil, errors.New("deployment role ownership does not match repository/environment; role left untouched")
	}
	account := strings.Split(arn, ":")[4]
	if !sameJSON(result.Role.AssumeRolePolicyDocument, githubTrust(account, subject)) {
		return "", nil, errors.New("deployment role trust was changed or shared; inspect before teardown")
	}
	var profiles struct {
		InstanceProfiles []any
		IsTruncated      bool
	}
	if _, err = a.call(&profiles, "iam", "list-instance-profiles-for-role", "--role-name", name); err != nil {
		return "", nil, err
	}
	if len(profiles.InstanceProfiles) > 0 || profiles.IsTruncated {
		return "", nil, errors.New("deployment role is attached to instance profiles; inspect before teardown")
	}
	var policies struct {
		PolicyNames []string
		IsTruncated bool
	}
	if _, err = a.call(&policies, "iam", "list-role-policies", "--role-name", name); err != nil {
		return "", nil, err
	}
	if policies.IsTruncated {
		return "", nil, errors.New("incomplete IAM policy listing")
	}
	allowed := map[string]bool{"attestra-state": true, "attestra-iam": true, "attestra-services": true, "attestra-dns": true, "attestra-state-kms": true}
	for _, policy := range policies.PolicyNames {
		if !allowed[policy] {
			return "", nil, errors.New("deployment role has unmanaged policies; inspect before teardown")
		}
	}
	var attached struct {
		AttachedPolicies []any
		IsTruncated      bool
	}
	if _, err = a.call(&attached, "iam", "list-attached-role-policies", "--role-name", name); err != nil {
		return "", nil, err
	}
	if len(attached.AttachedPolicies) > 0 || attached.IsTruncated {
		return "", nil, errors.New("deployment role has attached policies; inspect before teardown")
	}
	return name, policies.PolicyNames, nil
}
func deleteTeardownRole(a awsSetupAPI, arn, env string, m repositoryMetadata) error {
	name, policies, err := inspectTeardownRole(a, arn, env, m)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		if _, err = a.call(nil, "iam", "delete-role-policy", "--role-name", name, "--policy-name", policy); err != nil {
			return err
		}
	}
	_, err = a.call(nil, "iam", "delete-role", "--role-name", name)
	if err != nil {
		return err
	}
	found, err := a.call(nil, "iam", "get-role", "--role-name", name)
	if err != nil {
		return err
	}
	if found {
		return errors.New("role deletion not verified")
	}
	return nil
}

// Deletes evidence only after separate typed bucket confirmation. State is never purged.
func purgeEvidence(a awsSetupAPI, bucket, account, backend string) error {
	u, err := url.Parse(backend)
	if err != nil || bucket == "" || bucket == u.Host {
		return errors.New("refusing to purge an empty name or the state bucket")
	}
	for {
		var page struct {
			Versions, DeleteMarkers []struct{ Key, VersionId string }
			IsTruncated             bool
		}
		if _, err = a.call(&page, "s3api", "list-object-versions", "--bucket", bucket, "--expected-bucket-owner", account, "--max-keys", "1000", "--no-paginate"); err != nil {
			var awsErr *awsSetupError
			if errors.As(err, &awsErr) && awsErr.Code == "NoSuchBucket" {
				return nil
			}
			return err
		}
		objects := []map[string]string{}
		for _, v := range append(page.Versions, page.DeleteMarkers...) {
			objects = append(objects, map[string]string{"Key": v.Key, "VersionId": v.VersionId})
		}
		if len(objects) == 0 {
			if page.IsTruncated {
				return errors.New("incomplete S3 version listing")
			}
			break
		}
		var deleted struct{ Errors []any }
		// A payload file avoids Windows command-line length limits and never
		// prints object keys. Remove it immediately after the AWS call.
		payload, err := os.CreateTemp("", "attestra-evidence-delete-*.json")
		if err != nil {
			return err
		}
		payloadPath := payload.Name()
		_, writeErr := payload.WriteString(policyJSON(map[string]any{"Objects": objects, "Quiet": true}))
		closeErr := payload.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(payloadPath)
			return errors.New("cannot prepare evidence deletion payload")
		}
		_, err = a.call(&deleted, "s3api", "delete-objects", "--bucket", bucket, "--expected-bucket-owner", account, "--delete", "file://"+payloadPath)
		os.Remove(payloadPath)
		if err != nil {
			return err
		}
		if len(deleted.Errors) > 0 {
			return errors.New("S3 rejected evidence deletion; inspect object lock/permissions; no bypass attempted")
		}
	}
	return nil
}
