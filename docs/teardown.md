# Tear down an environment

This is a **permanent deletion** workflow. It removes the selected environment's application infrastructure/data, approved SES records in Cloudflare, setup-managed deployment role, Pulumi stack, and GitHub environment (including its variables, secrets and protection rules). No teardown runs automatically and there is no unattended `--yes` mode in this wizard.

From the repository root:

```text
git pull
.\teardown.bat
```

Linux/macOS: `./teardown.sh`. Direct Go: `go -C tools/deploy run . -teardown`.

## Credentials and prerequisites

Use the same GitHub token permissions as setup: Administration and Environments write, Actions read, Metadata read. Supply an AWS administrator identity through SSO, browser login, or access-key credentials. It needs permission to delete the stack resources and deployment role; the script refuses to use the deployment role itself. Enter the original stack passphrase. Cloudflare cleanup is optional: choose **Skip Cloudflare** before the token prompt to leave SES DNS records in place and continue tearing down AWS and GitHub. No Cloudflare token or API requests are needed when skipped. To clean up DNS automatically, use a zone-scoped token with Zone Read and DNS Edit. Tokens and passphrases are never saved in the teardown checkpoint.

Stop all local deployments and finish/cancel pending GitHub deployment runs first. The wizard checks for active/waiting Deploy workflow runs across the repository, but cannot prevent someone starting a new run afterward. Keep the environment in a maintenance window until teardown finishes.

## Removal sequence

1. Enter the repository and exact environment name. The script reads its saved GitHub variables, verifies the AWS account/region/backend/stack, and refuses a role or stack referenced by another GitHub environment in this repository.
2. Before destruction, capture the SES TXT/DKIM values and application evidence bucket from Pulumi state. Select the Cloudflare zone and review exact matching record IDs/values. Additional TXT values are retained. Conflicting CNAMEs stop the operation. If the stack manages its SES verification record through Route53, Pulumi handles those DNS records and Cloudflare cleanup is skipped.
3. Review `pulumi destroy --preview-only`. Type the environment name to authorize permanent teardown. Confirm that no other service uses the SES identity or selected DNS records; the script cannot discover other repositories or outside consumers.
4. If ID evidence exists, choose whether to purge its bucket. Purging requires typing the **bucket name** separately and permanently deletes every object version and delete marker. The state bucket can never be purged through this option. If you decline, a nonempty evidence bucket will block Pulumi destruction. Export anything you need before confirming. Object-lock/access errors stop cleanup; retention is never bypassed.
5. Run Pulumi destroy and verify that no application resources remain. Only then delete the approved Cloudflare records, remove the setup-owned deployment role and known inline policies, and remove the empty stack without `--force`.
6. Delete and verify removal of the GitHub environment last. Keep the progress file and encrypted YAML backup privately. Review and commit the removed `infra/Pulumi.<environment>.yaml` so Actions cannot reuse stale configuration.

The Cloudflare zone, unrelated DNS, shared GitHub OIDC provider, and **S3 state bucket with retained history/versioned objects** are deliberately retained. The script does not delete the repository, workflow files, website hosting/DNS, AWS account, or external services. AWS resources outside the Pulumi stack, such as automatically created Lambda log groups, can also remain. State storage continues to incur any applicable storage costs until you separately decide to remove it.

## Resume after a failure

Rerun the same script with the same repository/environment. An ignored `teardown.<environment>.local.json` file records the original target, exact DNS record IDs/values, and completed phases. It contains no credentials. Successful deletions are permanent and are never rolled back. A partial DNS or IAM failure can resume without recreating resources. GitHub configuration remains available until the last stage.

Do not delete or edit this progress file midway: once the SES identity is destroyed, its DNS tokens may no longer be recoverable from AWS. If Windows checkpoint replacement was interrupted and only `.bak` remains, restore it to the original `.json` filename before resuming. A `.lock` file prevents two teardown processes using the same checkpoint; after a crash, verify the old process is stopped before removing the stale lock.

The skip choice is saved as `DNSSkipped`, separately from completed DNS deletion, and is preserved when resuming. Remaining DNS records need manual cleanup; their expected names/values and any previously approved IDs remain in the checkpoint. Skipping after an earlier partial cleanup does not restore deleted records. The final summary explicitly reports the skip.

A completed checkpoint blocks another teardown. If you later recreate the environment, archive the completed checkpoint first so the next teardown reads fresh DNS/resource identities. Existing stack configuration and GitHub settings must match the saved target; changed targets stop the script for review.

## Cloudflare request errors

AWS SSO login and Cloudflare authentication are separate. Paste only the Cloudflare API token value when prompted, without `Bearer`, quotes, or an entire curl command. Use an API token with Zone Read and DNS Edit for the selected zone.

An HTTP 400 alone does not establish a permissions problem. The wizard reports the failed method/path and Cloudflare's error codes and messages (including nested errors), with the entered token redacted. Raw response bodies and query strings are omitted. For 401/403, check token validity and zone permissions; for 429, wait before retrying.

If a Cloudflare read fails before the destruction preview and confirmation, no teardown deletion has started in that invocation. Pull the latest script and rerun with the same environment. Preserve any checkpoint from earlier attempts. If it still fails, share the new error line, never your credentials.
