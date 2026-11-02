package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"
)

// Recovery deletion is deliberately a separate phase. Preflight ALL recovery
// objects and keys before deleting ANY bucket; schedule keys only after every
// bucket is verified gone. The receipt preserves key identities across retries.
func (n *nukeWorkflow) cleanupRecovery() {
	if !nukeSuccessful(n.receipt.Results) || n.persistErr != nil {
		return
	}
	for _, b := range n.receipt.Backends {
		a := n.api(b.Region)
		_, err := a.Call(nil, "s3api", "head-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
		if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
			continue
		}
		if err != nil {
			n.failure("Recovery preflight "+b.Bucket, err)
			continue
		}
		t := nukeTarget{Backend: "s3://" + b.Bucket, Region: b.Region, Project: "attestra-auth-email"}
		var r commandRunner = n.runner(t)
		if n.runnerForTarget != nil {
			r = n.runnerForTarget(t)
		}
		data, err := r.Exec(n.dir(t), true, "pulumi", "stack", "ls", "--all", "--json", "--non-interactive")
		var stacks []json.RawMessage
		if err == nil && (json.Unmarshal(data, &stacks) != nil || stacks == nil) {
			err = errors.New("invalid all-project stack listing")
		}
		if err == nil && len(stacks) > 0 {
			err = errors.New("state bucket contains remaining or foreign-project stacks")
		}
		if err != nil {
			n.failure("Recovery scope "+b.Bucket, err)
			continue
		}
		var objects struct{ Versions, DeleteMarkers []struct{ Key string } }
		if _, err = a.Call(&objects, "s3api", "list-object-versions", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account); err != nil {
			n.failure("Recovery object inventory "+b.Bucket, err)
			continue
		}
		keys := []string{}
		for _, o := range append(objects.Versions, objects.DeleteMarkers...) {
			keys = append(keys, o.Key)
		}
		if err = nukeRecoveryObjectScope(keys); err != nil {
			n.failure("Recovery scope "+b.Bucket, err)
			continue
		}
		var encryption struct {
			ServerSideEncryptionConfiguration struct {
				Rules []struct {
					ApplyServerSideEncryptionByDefault struct{ KMSMasterKeyID string }
				}
			}
		}
		_, err = a.Call(&encryption, "s3api", "get-bucket-encryption", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
		if err != nil && !nukeMissing(err, "ServerSideEncryptionConfigurationNotFoundError") {
			n.failure("Recovery encryption "+b.Bucket, err)
			continue
		}
		if err == nil && len(encryption.ServerSideEncryptionConfiguration.Rules) == 0 {
			n.failure("Recovery encryption "+b.Bucket, errors.New("incomplete bucket encryption response; key inventory cannot be certified"))
			continue
		}
		for _, rule := range encryption.ServerSideEncryptionConfiguration.Rules {
			if id := rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID; id != "" {
				key := nukeKey{id, b.Region}
				if !slices.Contains(n.receipt.Keys, key) {
					n.receipt.Keys = append(n.receipt.Keys, key)
				}
			}
		}
	}
	if !nukeSuccessful(n.receipt.Results) {
		return
	}
	customerKeys := []nukeKey{}
	for _, key := range n.receipt.Keys {
		metadata, found, err := nukeDescribeKey(n.api(key.Region), key.ID)
		if err != nil {
			n.failure("KMS preflight "+key.ID, err)
			continue
		}
		if !found {
			n.record(nukeResult{"KMS " + key.ID, "ABSENT", "key already absent"})
			continue
		}
		if metadata.KeyManager == "AWS" {
			n.record(nukeResult{"KMS " + key.ID, "INFO", "AWS-managed key; AWS owns its lifecycle"})
			continue
		}
		if metadata.KeyManager != "CUSTOMER" || !strings.HasPrefix(metadata.Arn, "arn:aws:kms:"+key.Region+":"+n.receipt.Account+":key/") {
			n.failure("KMS preflight "+key.ID, errors.New("cannot establish customer key identity in this account"))
			continue
		}
		key.ID = metadata.Arn
		if metadata.KeyState == "PendingDeletion" {
			n.record(nukeResult{"KMS " + key.ID, "SCHEDULED", "already pending deletion; AWS deletion date " + nukeDeletionDate(metadata.DeletionDate)})
			continue
		}
		confirm := n.confirmKey
		if metadata.KeyState != "Enabled" && metadata.KeyState != "Disabled" && metadata.KeyState != "PendingImport" {
			n.failure("KMS preflight "+key.ID, errors.New("key is not eligible for scheduling deletion; inspect its state and multi-region replicas"))
			continue
		}
		if metadata.MultiRegionConfiguration.MultiRegionKeyType == "PRIMARY" && len(metadata.MultiRegionConfiguration.ReplicaKeys) > 0 {
			n.failure("KMS preflight "+key.ID, errors.New("multi-region primary still has replicas; recovery artifacts retained"))
			continue
		}
		if confirm == nil {
			confirm = typedConfirmation
		}
		if err = confirm("Confirm this KMS key is exclusive to this project; shared keys must be retained", "delete key "+key.ID); err != nil {
			n.failure("KMS ownership "+key.ID, err)
			continue
		}
		if !slices.Contains(customerKeys, key) {
			customerKeys = append(customerKeys, key)
		}
	}
	if !nukeSuccessful(n.receipt.Results) {
		return
	}
	// Persist ALL discovered keys before the first bucket mutation. Losing the
	// bucket must not also lose the key IDs needed by a resumed cleanup.
	if err := n.save(); err != nil {
		n.persistErr = err
		return
	}
	for _, b := range n.receipt.Backends {
		a := n.api(b.Region)
		n.record(nukeStep("State bucket "+b.Bucket, func() (bool, error) {
			found, err := a.Call(nil, "s3api", "head-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
			if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
				return false, nil
			}
			return found, err
		}, func() error {
			// purgeEvidence prevents purging its supplied backend. Here this
			// bucket was explicitly authorized as the FINAL recovery target.
			if err := purgeEvidence(a, b.Bucket, n.receipt.Account, "s3://attestra-nuke-no-live-backend"); err != nil {
				return err
			}
			_, err := a.Call(nil, "s3api", "delete-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
			return err
		}))
		if !nukeSuccessful(n.receipt.Results) || n.persistErr != nil {
			return
		}
	}
	for _, key := range customerKeys {
		a := n.api(key.Region)
		if _, err := a.Call(nil, "kms", "schedule-key-deletion", "--key-id", key.ID, "--pending-window-in-days", "7"); err != nil {
			n.failure("KMS "+key.ID, err)
			continue
		}
		metadata, found, err := nukeDescribeKey(a, key.ID)
		if err != nil {
			n.failure("KMS deletion verification "+key.ID, err)
			continue
		}
		if !found {
			n.record(nukeResult{"KMS " + key.ID, "ABSENT", "key absence verified"})
			continue
		}
		if metadata.KeyState != "PendingDeletion" {
			n.record(nukeResult{"KMS " + key.ID, "UNVERIFIED", "key not in PendingDeletion; inspect multi-region replicas or AWS permissions"})
			continue
		}
		n.record(nukeResult{"KMS " + key.ID, "SCHEDULED", "AWS deletion scheduled, 7-day waiting period; deletion date " + nukeDeletionDate(metadata.DeletionDate)})
	}
}

type nukeKeyMetadata struct {
	Arn, KeyManager, KeyState string
	DeletionDate              json.RawMessage
	MultiRegionConfiguration  struct {
		MultiRegionKeyType string
		ReplicaKeys        []json.RawMessage
	}
}

func nukeDeletionDate(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var epoch float64
	if json.Unmarshal(raw, &epoch) == nil && epoch > 0 {
		return time.Unix(int64(epoch), 0).UTC().Format(time.RFC3339)
	}
	return "not returned by AWS; inspect PendingDeletion metadata"
}

func (n *nukeWorkflow) reportRetainedRecovery() {
	for _, b := range n.receipt.Backends {
		_, err := n.api(b.Region).Call(nil, "s3api", "head-bucket", "--bucket", b.Bucket, "--expected-bucket-owner", n.receipt.Account)
		if nukeMissing(err, "NoSuchBucket", "404", "NotFound") {
			n.record(nukeResult{"State bucket " + b.Bucket, "ABSENT", "bucket absence verified"})
		} else if err != nil {
			n.record(nukeResult{"State bucket " + b.Bucket, "UNVERIFIED", err.Error()})
		} else {
			n.record(nukeResult{"State bucket " + b.Bucket, "RETAINED", "not deleted: earlier cleanup or recovery preflight was unsuccessful"})
		}
	}
	for _, key := range n.receipt.Keys {
		m, found, err := nukeDescribeKey(n.api(key.Region), key.ID)
		if err != nil {
			n.record(nukeResult{"KMS " + key.ID, "UNVERIFIED", err.Error()})
		} else if !found {
			n.record(nukeResult{"KMS " + key.ID, "ABSENT", "key absence verified"})
		} else if m.KeyState == "PendingDeletion" {
			n.record(nukeResult{"KMS " + key.ID, "SCHEDULED", "AWS deletion date " + nukeDeletionDate(m.DeletionDate)})
		} else if m.KeyManager == "AWS" {
			n.record(nukeResult{"KMS " + key.ID, "INFO", "AWS-managed key; AWS owns its lifecycle"})
		} else {
			n.record(nukeResult{"KMS " + key.ID, "RETAINED", "not scheduled: earlier cleanup or recovery preflight was unsuccessful"})
		}
	}
}

func (n *nukeWorkflow) printSummary() {
	counts := map[string]int{}
	for _, r := range n.receipt.Results {
		counts[r.Status]++
	}
	fmt.Println("\nNuke report summary (operations and resource checks)")
	for _, status := range []string{"DELETED", "ABSENT", "SCHEDULED", "REMAINS", "RETAINED", "UNVERIFIED", "FAILED", "INFO"} {
		if counts[status] > 0 {
			fmt.Printf("%s: %d\n", status, counts[status])
		}
	}
	fmt.Println("Confirmed ABSENT is successful. SCHEDULED means KMS is pending deletion, not yet destroyed.")
	fmt.Println("Full report and retry inventory:", n.path)
}

func nukeDescribeKey(a awsenv.API, id string) (nukeKeyMetadata, bool, error) {
	var result struct{ KeyMetadata nukeKeyMetadata }
	found, err := a.Call(&result, "kms", "describe-key", "--key-id", id)
	if nukeMissing(err, "NotFoundException") {
		return result.KeyMetadata, false, nil
	}
	if err == nil && (!found || result.KeyMetadata.Arn == "") {
		err = errors.New("incomplete KMS key metadata")
	}
	return result.KeyMetadata, found, err
}

func nukeRecoveryObjectScope(keys []string) error {
	for _, key := range keys {
		if key == ".pulumi/meta.yaml" || key == "meta.yaml" || key == "bootstrap.lock" || key == "bootstrap-ready.json" || key == "teardown-pending.json" {
			continue
		}
		parts := strings.Split(key, "/")
		if len(parts) >= 4 && parts[0] == ".pulumi" && slices.Contains([]string{"stacks", "history", "backups", "locks"}, parts[1]) && slices.Contains([]string{"attestra-auth-email", "attestra-index"}, parts[2]) {
			continue
		}
		return errors.New("bucket contains unrecognized or foreign-project recovery objects; retained for manual review")
	}
	return nil
}
