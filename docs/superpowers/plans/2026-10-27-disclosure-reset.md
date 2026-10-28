# Generic disclosure and environment reset implementation plan

Goal: implement the approved generic localized onboarding message and an admin-triggered environment data reset. Fixed document type id_card; no country/language matrix. User origin is entered after scanning, separately from the issuing country extracted from the document.

Architecture: runtime config carries en/es/fr disclosures; snapshots plus chosen language preserve consent. The existing authenticated review endpoint persists origin. The shared admin invokes a deployment-owned per-environment reset Lambda with IAM, never caller-supplied resource names. A separate control table and minute sweeper persist reset progress; only managers can start/reset, confirmation must equal the environment. Production resets are disabled unless explicitly enabled by infrastructure configuration.

Tasks:
- [x] Add disclosure contract, defaults, validation and snapshot/language tests; update admin form and runtime defaults. Commit checkpoint.
- [x] Add Android localized policy display and origin selection, API serialization/controller tests. Commit checkpoint.
- [x] Add restart-safe reset state machine and scoped AWS adapters. Snapshot original function concurrency before pausing; wait seven minutes for functions and upload URLs to drain; purge queues and wait 60 seconds; delete users, table rows and all S3 versions in bounded batches; verify emptiness; restore concurrency only on success. Failures leave the environment paused and resumable. Test wrong confirmation, disabled reset, duplicate start, failure/retry and phase ordering.
- [x] Wire exact per-environment resource inventory/IAM, dedicated reset control storage and scheduled worker. Add shared admin IAM invocation, status/reset routes and typed confirmation UI. Preserve settings, admin pool, infra and reset audit. No actual reset or AWS deployment during development.
- [x] Run Go/race/vet/infra, Android tests/build and dashboard/docs checks, push checkpoints and open PRs.

Review focus: stale invocations after reset, lost responses and duplicate clicks, partial failure while paused, cross-environment access and production guard, legacy capture snapshots and unsupported locale fallback. Cloud owners must not deploy or manually mutate the target environment while reset is active. Database PITR/backups and service logs follow their own retention; reset is not a backup erasure operation.

Validation: Go race tests, vet, infra/infra-index tests, deploy-tool tests, all 18 Lambda archive checks, Android unit tests and debug/release builds, dashboard DOM smoke checks, and documentation build passed locally. AWS deployment/reset and physical-device validation were not performed.
