# Reset environment data

Deploy the backend and shared index/admin infrastructure before using this feature. The backend creates a dedicated reset worker and control table; the shared admin adds authenticated routes and narrowly scoped invocation permission. Reconcile existing setup IAM roles if deployment reports missing permission for the new reset resource prefixes.

Only an administrator with `manageGrants` for the selected environment can view or start a reset. Type the exact environment name and press Reset environment. Production (`prod`, `production`, or `environmentClass: production`) defaults to disabled; `environmentResetEnabled` is an explicit infrastructure override. Do not deploy, tear down, or manually mutate the environment during a reset.

## Scope

Reset deletes the selected environment’s application Cognito users, email proof rows, capture records (including extracted and reviewed data), all evidence object versions/delete markers, and queued capture work/dead letters. It preserves resource infrastructure, the separate admin Cognito pool, administrator grants, published runtime configuration/history, the shared environment index, and reset control/audit records. Database backups/PITR and service logs retain their configured retention; reset is not a backup-erasure operation. IAM resource inventory comes from Pulumi and cannot be supplied by the dashboard.

## Progress and recovery

The worker records original Lambda concurrency, pauses application/authentication and capture functions, waits seven minutes for active requests and signed upload URLs to drain, purges queues and waits at least 60 seconds, deletes data in bounded pages, and restores original concurrency. Expect at least eight minutes plus scheduling and deletion time. The dashboard polls progress; closing it does not cancel the job. Existing access tokens cannot access capture routes during reset or after the reset cutoff; users must enroll/sign in again.

A failed step leaves progress durable and the environment unavailable or partially paused. Fix the underlying AWS permission/quota/service error, refresh status, retype the environment name and choose Resume reset. A restore failure attempts to pause again and resumes with another drain/clear pass. Do not manually restore functions while clearing data. If interruption leaves a lease, the scheduled worker can retry after 90 seconds. Retrying the same operation does not start another reset. There is no automatic rollback of deleted data.

The control table keeps a latest state and an audit record for each reset operation, with administrator identity, timing and phase. Resource names and concurrency recovery details are not returned by the API. Inspect CloudWatch and AWS permissions for operational failures; the dashboard exposes only sanitized error codes.

## API and validation

`GET /api/environments/{environment}/reset` returns enabled state and progress. `POST` accepts only `{action, confirmation, operation}` with action `start` or `resume`; actor and environment come from verified authorization. The shared backend invokes `attestra-reset-<environment>` using IAM. No browser AWS credentials are used.

Local tests cover authorization, exact confirmation, duplicate requests, leases, phase timing, partial failure/recovery, original concurrency restoration, object-version deletion and old-token rejection. No live AWS reset was performed during implementation. Before relying on the feature, run an explicitly authorized smoke reset in a disposable environment and verify its data is empty while admin access and settings remain intact.
