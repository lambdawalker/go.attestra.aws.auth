# Attestra environment tools

This Go module contains the setup, deployment, credential management, health-check, Android export, and teardown commands. Run them through the repository's `.bat` / `.sh` launchers or the existing entry point:

```sh
go -C tools/deploy run . -bootstrap
go -C tools/deploy run . -teardown
go -C tools/deploy run . -teardown -nuke
go -C tools/deploy run . -teardown-index
go -C tools/deploy run . -manage-credentials
```

The launchers supply the repository path explicitly. Direct commands retain the existing `-repo-root` option and default. `main.go` only calls the workflow entry point and handles process exit. GitHub Actions uses the same executable.

## Package map

All paths below are relative to this directory.

| Directory | Responsibility |
| --- | --- |
| `internal/workflow` | CLI dispatch, setup/deploy/teardown sequencing, checkpoints, AWS login renewal, capacity policy, health checks, and deployment summaries |
| `internal/aws` | AWS CLI transport, redacted errors, and subprocess environment filtering |
| `internal/build` | Parallel Lambda compilation, archive packaging, and archive checks |
| `internal/dns` | Cloudflare API access, DNS planning, ownership comparisons, and approved record deletion |
| `internal/github` | GitHub environments, encrypted secret submission, and deployment/sharing checks |
| `internal/vault` | Credential encryption, file format, passphrase validation/rotation, and key-buffer cleanup |
| `internal/registry` | Signed environment-index requests, credential-provider refresh, and request retries |
| `internal/android` | Public Android properties-file serialization |
| `internal/ui` | Shared terminal prompts, environment picker, and environment-name validation |

Workflow code composes these packages. Reusable packages must not import `workflow`; supply data or a narrow callback/interface instead. For example, AWS CLI transport accepts a renewal-error callback, while the workflow owns interactive authentication. Route53 configuration remains in workflow because it is applied through the Pulumi deployment stages. Index infrastructure setup and deployment receipts likewise remain with their workflows.

Keep unit tests beside their implementations. Tests that exercise several packages together belong in `internal/workflow`. Launcher tests stay at the module root so they continue testing the real repository launchers.

## Verification

From the repository root:

```sh
go -C tools/deploy test -race ./...
go -C tools/deploy vet ./...
go -C tools/deploy build -o attestra-deploy ./
```

Use `./...` when testing: the command package alone does not include the internal package tests. Tests use local fixtures and fake services; they do not deploy or tear down cloud environments.

The package layout does not change CLI flags, checkpoint JSON, vault encryption or location, Pulumi configuration, Android export keys, or the GitHub workflow entry point. See [deployment instructions](../../docs/deployment.md), [first deployment](../../docs/first-deployment.md), and [teardown](../../docs/teardown.md).

Windows credential renewal uses an unquoted absolute helper path because the AWS Go SDK's `cmd.exe` invocation does not preserve quoted executable paths. Paths containing spaces use the Windows short-path alias. If that filesystem has short names disabled, the wizard stops with instructions to set `TEMP` and `TMP` to an existing path without spaces (for example, `C:\attestra-temp`) and rerun. If `GOTMPDIR` is set, update it too; for a prebuilt executable, move it to such a directory. It never changes the original AWS profile.

## Generated local state

Wizard progress files live in ignored `.attestra/` at the repository root:

- `bootstrap.<environment>.local.json`: non-secret setup choices, progress, health/completion receipts and configuration fingerprints.
- `teardown.<environment>.local.json`: teardown progress, resource inventory and deletion verification.
- `index-publication.<environment>.local.json`: pending publication/recovery receipts.
- `teardown-history/`: archived completed teardown records and their encrypted configuration backups.

Their `.bak`, `.tmp` and `.lock` siblings stay alongside them. Stop any running setup/deployment/teardown before upgrading. On the next command, old root-level files are moved intact. Conflicting old/new files or existing operation locks stop migration for inspection; no history is overwritten. Interrupted moves resume on rerun.

Keep `.attestra/` when resuming interrupted operations. These files contain no credential values but include infrastructure identifiers and recovery metadata; keep them private. The optional encrypted credential vault stays in its existing user-specific location. Build artifacts already live in `dist/`, public Android exports in `android-config/`, and Pulumi configuration stays in `infra/` and `infra-index/`.

Shared-index teardown has its own [runbook](../../docs/teardown-index.md) and private `.attestra/teardown-index.<account>.<region>.local.json` checkpoint. It does not replace environment teardown.

## Nuke all environments and index/admin

Run `./deploy.sh -teardown -nuke` or `deploy.bat -teardown -nuke` from the repository root. The existing `scripts/teardown.sh -nuke` / `scripts/teardown.bat -nuke` launchers work too. Running ordinary teardown also offers a **Nuke ALL environments** scope. Nuke requires an interactive administrator session and typed confirmation of the repository and AWS account; it has no unattended mode.

After a nuke, run `deploy.bat -teardown -nuke -check` (or `./deploy.sh -teardown -nuke -check`) to verify artifacts using the last saved report. This checks failed, absent, deleted, retained, remaining and unverifiable entries again, without deleting resources or invoking recovery cleanup. It uses captured resource IDs and regions even if their Pulumi stacks are gone, and checks each repeated artifact once. Previously absent resources that have reappeared are reported as `REMAINS`.

The check authenticates to the same AWS account and validates the report's repository/account identity. It does not discover new resources. GitHub and Cloudflare read tokens are optional; unavailable access, permission errors, unsupported result identities and incomplete responses produce `UNVERIFIED`, never assumed absence. Shared infrastructure and informational operation entries remain outside the check. AWS CLI is required; Pulumi is needed only to check stacks whose state bucket still exists. A missing report is an error.

Fresh results are saved as `CheckResults` with `CheckedAt` in `.attestra/nuke.<account>.local.json`; original `Results` and inventories remain available for retries. Each result includes its previous status and a current `ABSENT`, `REMAINS` or `UNVERIFIED` status. KMS keys pending deletion still exist and are reported as `REMAINS` with their state and deletion date. The command exits unsuccessfully if any checked artifact remains, cannot be verified, or the report cannot be saved. Run the normal nuke command separately when you want to retry cleanup; a check never releases the recovery deletion gate.

For follow-up checks, use `deploy.bat -teardown -nuke -fast-check` (or `./deploy.sh -teardown -nuke -fast-check`). This implies check mode and queries **only artifacts marked `REMAINS` in the latest `CheckResults`**. It requires a previous check; it never falls back to checking every artifact. Artifacts last marked `ABSENT` or `UNVERIFIED` are skipped. If nothing was last known to remain, no artifact queries are made. All check modes remain non-destructive.

Fast checks update the selected entries while preserving skipped evidence. `CheckScope` is `remaining-only` and `CheckedArtifacts` identifies the entries refreshed at `CheckedAt`; a full `-check` sets `CheckScope` to `all`. The console reports refreshed and skipped counts. Skipped `UNVERIFIED` entries still prevent an overall successful result; use a full `-check` to revisit them or detect resources that reappeared after being absent. Repeated fast checks progressively narrow the queried set as remaining artifacts disappear. A new nuke clears previous check metadata.

Nuke discovers application environments from GitHub variables, local setup/deployment/teardown records, the saved nuke report, and project stacks in owned `attestra-state-*` / `attestra-index-state-<account>-*` buckets. It reviews the complete discovered scope, then destroys application stacks followed by the shared **index and admin** stack. It captures resource IDs before refreshing missing resources, removes managed Pulumi protection/retain-on-delete guards, purges application bucket versions, disables table deletion protection, and uses Pulumi's continue-on-error destroy. Independent targets, DNS, deployment-role and GitHub cleanup continue after failures. It also cleans up Lambda log groups and backups of the captured DynamoDB tables. AWS Backup recovery points are reported for manual cleanup through AWS Backup.

Stop all local and GitHub deployments until cleanup finishes. The script checks active deployment workflow runs and never steals Pulumi locks. It does not sweep unrelated account resources. It retains shared GitHub OIDC providers, DNS zones, provider credentials/vaults and local reports. External resources, foreign projects, provider account/endpoint overrides, unfamiliar recovery-bucket contents and prefixed backends require manual review and block recovery deletion.

**State buckets and KMS keys are the final phase.** Every earlier resource/cleanup result must be successful first. The script preflights all recovery buckets and keys, persists discovered key IDs, deletes and verifies all state buckets, and only then schedules exclusive customer-managed KMS keys with a seven-day waiting period. It discovers keys from bucket encryption and Pulumi `awskms://` secrets providers. Each customer key requires explicit confirmation that it is exclusive to this project. AWS-managed keys remain under AWS ownership. A failed or unverifiable earlier step preserves recovery buckets and keys; a bucket-deletion failure prevents subsequent bucket deletion and all KMS scheduling. Scheduling failure after bucket deletion is reported and can be retried using the saved key inventory.

The console prints each result plus totals. The private `.attestra/nuke.<account>.local.json` file contains the detailed report and inventories needed for retry:

| Status | Meaning | Successful? |
| --- | --- | --- |
| `DELETED` | Present before deletion; absence verified afterward | Yes |
| `ABSENT` | Already missing, with an authoritative read-back | **Yes** |
| `SCHEDULED` | KMS reports `PendingDeletion`; deletion date included | Yes, pending AWS's waiting period |
| `INFO` | Command diagnostic or explicitly excluded shared/AWS-owned infrastructure | Informational |
| `REMAINS` | Resource is still present after cleanup | No |
| `FAILED` | Operation or scope validation failed | No |
| `UNVERIFIED` | Permissions, connectivity, unsupported type or missing evidence prevents verification | No |
| `RETAINED` | Identified artifact was deliberately left behind; reason included | No for in-scope artifacts |

Confirmed absence is never a failure. A missing stack with no surviving pre-destroy inventory is different: the stack itself is `ABSENT`, but orphan AWS resources cannot be certified, so inventory is `UNVERIFIED` and recovery deletion stays blocked. Command errors during refresh/destroy remain visible as diagnostics; the final AWS and Pulumi read-backs determine the result. Rerun the same command with the same `.attestra/` directory to retry; completed/missing items are checked again rather than treated as errors. Do not delete the report until all cleanup and any KMS waiting period have been reviewed.

## Export values for the modular deployment wizard

From the repository root, run `./export-wizard.sh --environment dev`, or on
Windows `export-wizard.bat --environment dev`. Requires Python 3 and PyYAML
(`python -m pip install -r scripts/requirements-export.txt`).
It reads local `infra/Pulumi.<environment>.yaml` and
`infra-index/Pulumi.shared.yaml` without contacting a state backend or decrypting
secrets, plus `.attestra/deploy.<environment>.local.json` when available.
If a local stack file is absent, it falls back to read-only
`pulumi config --json --non-interactive` using the current Pulumi backend/session.
A failed fallback reports Pulumi stderr and recovery guidance instead of a traceback. Run once per environment; exports merge into
`~/.attestra/legacy-wizard.json` with private file permissions.

Exports include sender domain/address, AWS region/account when available,
base domain derived from the index domain, Android associations, environment
class, and saved profile/state base. The new wizard automatically skips questions
for these values. Missing settings still prompt. Flags and local wizard settings
win over exports. Legacy domains, certificate ownership, secrets and credential
vaults are excluded. This does not transfer infrastructure ownership or deploy.
Use `--output PATH` only to inspect/export elsewhere; the new wizard discovers
the default home-directory path automatically.

