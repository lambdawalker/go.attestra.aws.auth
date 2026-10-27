# Deployment, recovery and teardown

| Intent | Entry point | Authoritative runbook |
| --- | --- | --- |
| Create/resume an environment | `setup.bat` / `./setup.sh` | [First deployment](../first-deployment.md) |
| Build Lambda ZIPs only | `scripts/build.bat` / `./scripts/build.sh` | [Deployment](../deployment.md) |
| Preview/update existing application | `deploy.bat` / `./deploy.sh` or Deploy AWS action | [GitHub deployment](../github-deployment.md) |
| Replace tokens/change vault passphrase/delete saved credentials | `scripts/credentials.bat` / `./scripts/credentials.sh` | [Credential management](../deployment.md) |
| Remove one environment | `scripts/teardown.bat` / `./scripts/teardown.sh` | [Environment teardown](../teardown.md) |
| Remove shared index after all environments | `scripts/teardown-index.bat` / `./scripts/teardown-index.sh` | [Index teardown](../teardown-index.md) |

Setup is resumable, not transactional rollback. An error retains completed resources and private checkpoints in `.attestra/`. Do not delete state to repair a lock. After a process crash, establish that no writer remains before following the receipt-specific recovery procedure. A completed offline setup receipt is not evidence of current remote health; `-force-setup` reconciles services.

Application state uses a private, versioned bucket per environment. Shared index state uses its own account/region bucket. Both use Pulumi's S3 backend and passphrase secrets; no Pulumi Cloud API token is required. Preserve each stack's original passphrase. AWS temporary credentials can expire; profile-based credentials renew through an isolated CLI credential provider. Pasted credentials cannot renew themselves. See the [Windows helper-path limitation](../../tools/deploy/README.md).

Dev, QA and custom test environments default to shared Lambda capacity and two queue-worker invocations. Production requires explicit capacity and strict quota checking. Two workers means an event-source concurrency limit, not two permanently provisioned workers. Account/region capacity is shared across stacks; the [capacity runbook](../github-deployment.md#capture-concurrency-per-environment) explains configuration and fallback.

The index is reused across environments and updated after successful deployment. A public configuration hash is not a Git SHA. Failed publication retains a receipt; retry publication rather than redeploying blindly. IAM authentication proves authorized callers, not that a deployment actually happened. [Index recovery](../environment-index.md) explains stale locks and concurrent environments.

Final health checks inspect infrastructure, DNS/TLS, unauthenticated route behavior, CORS and index agreement without sending email or uploading evidence. Environment teardown retires its index entry and retains shared infrastructure. Index teardown refuses active registry rows/locks, verifies deletion and retains state/history. Keep deployments stopped throughout destructive operations; preflight scans are not a distributed freeze.
