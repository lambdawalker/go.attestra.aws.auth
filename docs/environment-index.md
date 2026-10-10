# Shared environment index

The index is a separate Go/Pulumi project in `infra-index/`, alongside the application project in `infra/`. Its default public endpoint is:

```text
https://index.attestrabond.com/v1/environments
```

For another base domain, setup uses `index.<base-domain>`. The first successful setup establishes the shared hostname and DNS provider; subsequent environments reuse those settings.

## Enable it

Pull the latest `main`, then run the full setup wizard for each existing environment:

```powershell
.\setup.bat -force-setup
```

Choose `dev`, then repeat for `qa` and any other environment that should appear in the index. New environments use the same flow. Environments are registered after successful deployment; merely creating a GitHub environment does not publish an entry.

The wizard:

1. Prepares the application's state and configuration as before.
2. Creates or reuses the private shared state bucket `attestra-index-state-<account>-<region>` and the `attestra-index/shared` Pulumi stack.
3. Asks for the shared index's Pulumi passphrase. Keep this separate stack's original passphrase for all environments and future reruns. The optional credential vault can save it alongside the application's passphrase; a different environment's vault may require entering it once.
4. Builds the registry Lambda, deploys certificate prerequisites, configures Route 53 or Cloudflare DNS, waits for ACM, and deploys the remaining index infrastructure.
5. Reuses a completed registry when its source hash matches. Changed registry source triggers another staged preview/update. A failed update does not write the completion marker, so the next run resumes it.
6. Adds an environment-specific `execute-api:Invoke` policy to that environment's GitHub deployment role and writes the registry connection settings into the application's Pulumi YAML.
7. Keeps IAM/GitHub configuration and application deployment under an environment lock and publishes only the allowlisted public outputs.

Review and commit both `infra/Pulumi.<environment>.yaml` and `infra-index/Pulumi.shared.yaml`. The shared stack's passphrase is not uploaded to GitHub: application workflows do not deploy shared infrastructure or need its state bucket. Run setup when changing the registry itself.

An unchanged, locally completed setup still returns before calling AWS, GitHub, or Cloudflare. This is an offline completion receipt, not a drift check. Use `-force-setup` when checking remote services. Pending publication receipts invalidate that shortcut.

## Infrastructure and scope

The shared project owns an HTTP API, a Go Lambda, an on-demand DynamoDB table with point-in-time recovery and Pulumi deletion protection, an ACM certificate, its custom domain, and Route 53 records when selected. Cloudflare records use the existing token flow and conflict/read-back checks. No Lambda concurrency is reserved for the index.

Application teardown retains all shared infrastructure, DNS, and shared state. Removing the last environment leaves a working endpoint with an empty array.

This initial implementation supports environments in **one AWS account and region per shared index**. It does not automatically federate separate AWS accounts or regions into one hostname. Cross-account production isolation needs a designated registry account and an explicit cross-account publication role before expanding this setup. Do not independently bootstrap the same hostname in another account/region.

Setup uses your administrative AWS session to provision the shared resources. Application deployment roles receive only environment-scoped registry API publication permission, not registry table access or shared state access. Existing application deployment policies still have broader regional infrastructure permissions; this feature is not a replacement for account-level isolation between untrusted environments.

## Public response

```json
{
  "schemaVersion": 1,
  "environments": [
    {
      "id": "dev",
      "configuration": {
        "apiUrl": "https://dev.api.attestrabond.com",
        "awsRegion": "us-east-2",
        "cognitoUserPoolId": "us-east-2_example",
        "cognitoClientId": "exampleclientid",
        "features": { "idCapture": false }
      },
      "configHash": "<sha256-of-canonical-public-configuration>",
      "revision": 1,
      "updatedAt": "2026-10-10T12:00:00Z"
    }
  ]
}
```

Only deployed API URL, region, public Cognito identifiers, and capture availability are published. AWS credentials, Pulumi secrets, proof keys, DNS tokens, evidence bucket names, and ID/identity data are excluded. All listed environments, including dev/QA, are publicly discoverable. GET supports browser CORS and returns `Cache-Control: no-store`.

`configHash` identifies the public configuration, not the Git commit. Identical configuration keeps its existing public hash, revision, and timestamp. Deploying new application code can therefore leave the public entry unchanged.

Android consumption of this endpoint is a separate client change. Until then, the existing Android configuration output still works.

## Concurrent deployments

Before changing an indexed application’s stack configuration or IAM/GitHub settings, setup acquires its lock. First-time setup acquires it as soon as the shared registry is ready, before IAM/GitHub configuration. Application deployments use the same AWS SigV4-authenticated endpoint. Setup and deploy call an AWS SigV4-authenticated endpoint:

```text
POST /v1/environments/<environment>/changes
```

DynamoDB conditional writes acquire a lock and increment that environment's revision. A second deployment to the same environment is rejected before changing resources. Different environments use different rows and proceed independently. GitHub's existing per-environment workflow concurrency also remains enabled.

Successful publication requires the exact token and revision that acquired the lock. A stale writer cannot overwrite a newer deployment. Teardown acquires the same lock, destroys the application, then publishes a deletion tombstone while retaining the lock through DNS, IAM, stack, and GitHub cleanup. Only after all cleanup is recorded does it release the lock. The tombstone preserves revision history, so an old publication cannot restore a deleted entry. Intentionally setting up that environment again obtains a new revision.

There is no whole-index file to rewrite and no listener that probes every application's URL. Reads assemble the current published rows. This avoids lost updates between unrelated environments. A multi-environment read is not a transactionally consistent snapshot across all rows.

Use the provided setup/deploy/teardown tools. Direct `pulumi up`, manual AWS edits, and older versions of these tools bypass index synchronization. A deployment or teardown failure may temporarily leave the last successfully published configuration visible; retry the pending operation. The index describes published configuration, not live service health.

## Recover publication without redeploying

If AWS deployment succeeds but publication fails, the tool retains `.attestra/index-publication.<environment>.local.json`. It contains public configuration and a recovery receipt, not AWS credentials. Keep it private and never commit it; possession alone does not grant API access.

Locally, with the same checkout and receipt:

```powershell
.\deploy.bat -stack qa -backend s3://YOUR-QA-STATE-BUCKET -sso -profile attestra -publish-index
```

On GitHub, the failed run uploads an `index-recovery-<environment>` artifact, retained for 90 days. Start **Deploy AWS [Pulumi S3]**, choose the same environment, choose **publish**, and supply the failed run's numeric ID as `receipt_run_id`. The action restores the receipt and retries publication without building or applying infrastructure. Do this promptly; do not let the recovery artifact expire.

Do not mark an interrupted deployment as successful by editing its receipt. A receipt with `Deployed: false` requires inspection of the workflow and Pulumi update status first.

## Interrupted operations and locks

Locks deliberately do not expire automatically: an old process must not continue deploying after a newer process takes its place.

For an interrupted application deployment:

1. Stop/verify completion of its local process and GitHub run. Inspect Pulumi's update status. Do not release a lock while any writer is still running.
2. Restore its receipt artifact locally if necessary. If a local `.attestra/index-publication.<environment>.local.json.lock` remains after a process crash, remove **only that local `.lock` file** after confirming the process has stopped; keep the JSON receipt.
3. If deployment completed successfully and `Deployed` is true, use `-publish-index`.
4. Otherwise use the explicit recovery command, which asks you to type the environment name:

```powershell
.\deploy.bat -stack qa -backend s3://YOUR-QA-STATE-BUCKET -sso -profile attestra -release-index-lock
```

This releases only the lock matching the saved token/revision; it does not publish potentially incomplete outputs. Rerun setup/deploy to reconcile the application. A token saved before a lost acquisition response can safely retry acquisition before releasing it.

If teardown was interrupted, rerun teardown with its `.attestra/teardown.<environment>.local.json` checkpoint. It retains its registry receipt and resumes deletion. Do not use a deployment receipt to override a teardown lock.

If the receipt is irretrievably lost, an AWS administrator must inspect the registry table and all deployment processes before repairing the specific environment row. Increment its `revision` and remove `lock` in one conditional update matching the inspected revision and token. Do not delete the row or reset its revision; retain any existing entry/tombstone. This invalidates old receipts. Normal deployment tools never perform this administrative override.

Shared-infrastructure setup has a separate conditional S3 lock, `.attestra/bootstrap.lock`, in the shared state bucket. After an abrupt crash, verify no setup process or shared Pulumi update is running, then remove that exact lock object to resume. Never delete Pulumi state to resolve a lock. If shared infrastructure was changed outside setup, inspect it and remove only `bootstrap-ready.json` to force the next setup to reconcile it; keep the stack state and configuration.

## Validation

`go test ./...` covers registry conditional-write semantics, concurrent acquisition, idempotent retries, stale publication rejection, deletion, and unchanged configuration. `go -C tools/deploy test ./...` covers signed requests, endpoint validation, recovery receipt retention/retry, deployment-failure release, and source fingerprints. `go -C infra-index test ./...` uses Pulumi mocks to check IAM-protected writes, public reads, both DNS branches, unreserved Lambda concurrency, and shared table protection.

These checks do not substitute for a first deployment in your AWS account; no live infrastructure is created by the tests.

### Readiness troubleshooting

Setup checks the direct API endpoint and then the public index hostname. Each
check prints its URL and reports HTTP status, invalid JSON/schema, or the network
error while waiting. On timeout or cancellation it retains the last failure and
all infrastructure; rerun setup to resume. Lambda START/END/REPORT lines alone
do not establish HTTP success. A healthy index returns HTTP 200 with
`schemaVersion: 1`; an empty environments list is valid before publication.

For permanent removal of the shared infrastructure after retiring every environment, see [shared index teardown](teardown-index.md).
