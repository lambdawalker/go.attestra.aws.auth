# Tear down an environment

This is a **permanent deletion** workflow. It removes the selected environment's application infrastructure/data, approved SES records in Cloudflare, setup-managed deployment role, Pulumi stack, and GitHub environment (including its variables, secrets and protection rules). No teardown runs automatically and there is no unattended `--yes` mode in this wizard.

From the repository root:

```text
git pull
.\teardown.bat
```

Linux/macOS: `./teardown.sh`. Direct Go: `go -C tools/deploy run . -teardown`.

The **first question is Environment to tear down**, with dev, qa, prod, locally known environments, and a custom-name option. Local teardown checkpoints are included so interrupted custom-environment removals remain selectable. Repository and credentials are requested only afterward; a saved teardown/setup repository is used as the default.

## Credentials and prerequisites

Use the same GitHub token permissions as setup: Administration and Environments write, Actions read, Metadata read. Supply an AWS administrator identity through SSO, browser login, or access-key credentials. It needs permission to delete the stack resources and deployment role; the script refuses to use the deployment role itself. Enter the original stack passphrase. Cloudflare cleanup is optional: choose **Skip Cloudflare** before the token prompt to leave SES DNS records in place and continue tearing down AWS and GitHub. No Cloudflare token or API requests are needed when skipped. To clean up DNS automatically, use a zone-scoped token with Zone Read and DNS Edit. Tokens and passphrases are never saved in the teardown checkpoint. If the selected repository/environment has a credential vault, you can unlock it and reuse its GitHub/Cloudflare tokens, manual AWS credentials, and application Pulumi passphrase. SSO/browser credentials still use the refreshable AWS profile provider. Expired saved credentials can be replaced separately with `credentials.bat` or `credentials.sh`.

Stop all local deployments and finish/cancel pending GitHub deployment runs first. The wizard checks for active/waiting Deploy workflow runs across the repository, but cannot prevent someone starting a new run afterward. Keep the environment in a maintenance window until teardown finishes.

## Removal sequence

1. Select the environment first, then confirm the repository. The script reads its saved GitHub variables, verifies the AWS account/region/backend/stack, and refuses a role or stack referenced by another GitHub environment in this repository.
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

API custom domains are included in teardown: Pulumi removes the API mapping, custom domain and ACM certificate. Before destruction, the wizard captures the API CNAME and ACM validation CNAME from state, alongside SES DNS evidence, for exact-match Cloudflare cleanup. Only approve deletion if those validation records are not used by another certificate/service. The selected Cloudflare zone must cover every captured record. Route53-managed records are removed by Pulumi. **Skip Cloudflare** retains both SES and API DNS records for manual cleanup. The shared API Gateway service-linked role is retained.


## Local cleanup and shared infrastructure

After cloud and GitHub cleanup succeeds, teardown removes the selected environment's `bootstrap.<environment>.local.json` and `.bak` (including saved health/completion summaries), the legacy `bootstrap.local.json` record for dev when present, and the default `android-config/<environment>.properties` export. Other environments are untouched. Setup records for another repository stop local cleanup for review. Copies exported to custom paths, installed Android apps, and existing builds are not changed.

Before final authorization, the wizard separately asks whether to delete the selected credential vault; the default is **keep**, because it can contain the shared-index passphrase. This decision is checkpointed for retries. Vault deletion does not revoke provider tokens or clear AWS CLI login caches. The teardown checkpoint and encrypted Pulumi YAML backup remain available for recovery/audit.

The environment is removed from the shared index, but the shared index Pulumi project, service, DNS, state bucket, and shared OIDC provider remain. Production capacity settings do not trigger reservation allocation during teardown; the normal destruction preview and typed confirmations still apply.
