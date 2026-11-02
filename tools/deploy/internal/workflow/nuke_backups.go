package workflow

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

func (n *nukeWorkflow) captureStateKey(data []byte, region string) error {
	var state struct {
		Deployment struct {
			Secrets struct {
				Type  string
				State json.RawMessage
			} `json:"secrets_providers"`
		}
	}
	if json.Unmarshal(data, &state) != nil {
		return errors.New("invalid secrets provider inventory")
	}
	if state.Deployment.Secrets.Type == "" || state.Deployment.Secrets.Type == "passphrase" {
		return nil
	}
	var config struct{ URL string }
	raw := state.Deployment.Secrets.State
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = []byte(encoded)
	}
	if json.Unmarshal(raw, &config) != nil || !strings.HasPrefix(config.URL, "awskms://") {
		return errors.New("unrecognized cloud secrets provider; preserve state for manual key review")
	}
	parts := strings.SplitN(strings.TrimPrefix(config.URL, "awskms://"), "?", 2)
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "" {
		return errors.New("invalid KMS secrets provider key")
	}
	if len(parts) == 2 {
		q, err := url.ParseQuery(parts[1])
		if err != nil {
			return errors.New("invalid KMS secrets provider URL")
		}
		if q.Get("region") != "" {
			region = q.Get("region")
		}
	}
	key := nukeKey{id, region}
	if !slices.Contains(n.receipt.Keys, key) {
		n.receipt.Keys = append(n.receipt.Keys, key)
	}
	return nil
}

func (n *nukeWorkflow) cleanupTableBackups(region, table string) {
	a := n.api(region)
	var result struct {
		BackupSummaries *[]struct{ BackupArn, BackupType, BackupStatus, TableName string }
	}
	_, err := a.Call(&result, "dynamodb", "list-backups", "--table-name", table, "--backup-type", "ALL")
	if err != nil {
		n.failure("Backup inventory "+table, err)
		return
	}
	if result.BackupSummaries == nil {
		n.failure("Backup inventory "+table, errors.New("incomplete backup listing"))
		return
	}
	if len(*result.BackupSummaries) == 0 {
		n.record(nukeResult{"Backups for " + table, "ABSENT", "no DynamoDB backups listed"})
	}
	for _, b := range *result.BackupSummaries {
		if b.TableName != table || !strings.HasPrefix(b.BackupArn, "arn:aws:dynamodb:"+region+":"+n.receipt.Account+":table/"+table+"/backup/") {
			n.failure("Backup ownership "+table, errors.New("unexpected backup table/account identity"))
			continue
		}
		if b.BackupType == "AWS_BACKUP" {
			n.record(nukeResult{"Backup " + b.BackupArn, "RETAINED", "AWS Backup recovery point; remove it through AWS Backup before retrying final recovery cleanup"})
			continue
		}
		n.record(nukeStep("Backup "+b.BackupArn, func() (bool, error) {
			var body struct {
				BackupDescription struct{ BackupDetails struct{ BackupStatus string } }
			}
			_, err := a.Call(&body, "dynamodb", "describe-backup", "--backup-arn", b.BackupArn)
			if nukeMissing(err, "BackupNotFoundException") {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if body.BackupDescription.BackupDetails.BackupStatus == "" {
				return false, errors.New("incomplete backup description")
			}
			return body.BackupDescription.BackupDetails.BackupStatus != "DELETED", nil
		}, func() error {
			_, err := a.Call(nil, "dynamodb", "delete-backup", "--backup-arn", b.BackupArn)
			if err != nil {
				return err
			}
			// Deletion is asynchronous. A retained or inaccessible backup blocks
			// recovery deletion rather than being declared gone prematurely.
			for attempt := 0; attempt < 3; attempt++ {
				var body struct {
					BackupDescription struct{ BackupDetails struct{ BackupStatus string } }
				}
				_, err = a.Call(&body, "dynamodb", "describe-backup", "--backup-arn", b.BackupArn)
				if nukeMissing(err, "BackupNotFoundException") || err == nil && body.BackupDescription.BackupDetails.BackupStatus == "DELETED" {
					return nil
				}
				if err != nil {
					return err
				}
				time.Sleep(2 * time.Second)
			}
			return nil
		}))
	}
}
