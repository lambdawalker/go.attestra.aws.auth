# Nuke teardown implementation plan

**Goal:** `deploy -teardown -nuke` removes every discovered application environment and the shared index/admin, tolerating confirmed absence and continuing independent cleanup after failures.

**Architecture:** A separate Go workflow uses the existing AWS, GitHub, DNS and Pulumi adapters. It captures inventories before refresh, destroys application stacks before shared stacks, verifies each resource and persists a JSON report. Recovery buckets and customer-managed KMS keys form a final gated phase.

**User requirements:** All environments; index/admin included; confirmed missing items succeed; report destroyed versus not destroyed; state buckets and KMS deletion only if everything else succeeds.

## Tasks

- [x] Add tests for missing-resource classification, continuing independent steps, recovery-phase gating, and per-resource read-back outcomes.
- [x] Implement discovery from GitHub, local settings, receipts and project state buckets; review account and full scope before typed confirmation.
- [x] Implement inventory capture, refresh, deletion-guard removal, bucket purge, application/shared destroy, DNS, role, log and local cleanup with a persistent report.
- [x] Run the final recovery phase only when all earlier results are successful; verify bucket absence and KMS PendingDeletion state. Preserve recovery data on any failure.
- [x] Wire the CLI and existing teardown launchers, document commands and limitations, run cloud-free tests and independent code review.
- [ ] Push the implementation branch and open a pull request.

## Review focus

- Missing resources are success; access denied, malformed responses and timeouts are failures.
- Partial destroy does not stop other environments or the shared stack.
- Capture before refresh so missing resources are still reported.
- No state removal after a failure or unverifiable resource; no KMS scheduling until every state bucket is gone.
- Other Pulumi projects, external resources, shared KMS keys and shared OIDC/DNS zones are never silently claimed deleted.
