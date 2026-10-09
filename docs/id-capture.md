# ID capture

Implementation of the [onboarding capture design](https://github.com/lambdawalker/design.attestra/tree/main/auth/onboarding/id-capture). Capture does not parse document fields, verify authenticity, perform face matching, or authorize restricted features.

## Components

- `capture`: account-scoped state machine, upload/finalize idempotency, revision fences, bounded file checks and cleanup.
- `captureapi`: API Gateway HTTP v2 handler. Every route requires the Cognito JWT authorizer; the handler additionally requires `token_use=access` and `sub` from the verified authorizer context. Client-provided owners, keys and URLs are never accepted.
- `awscapture`: DynamoDB conditional writes and transactional ready outbox, private S3 SigV4 REST adapter, SQS dispatch using the existing AWS credential provider/signer. No new SDK modules or permanent credentials.
- `capture-worker`: version-specific reads, SHA-256/length checks, JPEG decoding with a 20 MP limit, metadata-free JPEG derivatives, ready publication and deletion.
- `capture-dispatcher`: each minute queries the durable work index and sends SQS jobs. Delivery is at least once; revision/generation/120-second leases prevent stale publication. The Lambda timeout is 90 seconds.

Durable validation/cleanup intent is stored **on the capture record** with its state transition, so no transaction can leave a frozen capture without work. This replaces a separate validation-outbox row with a co-located outbox. Ready publication uses a DynamoDB transaction containing the capture and an immutable `READY#...` event. There is deliberately no parsing consumer in this change. A future consumer must recheck the owned capture's current state, evidence version and retention before reading the pinned `Processed` manifest; historical events do not override cancellation/expiry.

## HTTP contract

All routes use `Authorization: Bearer <Cognito access token>`. JSON responses have `Cache-Control: no-store`. Operation keys are 16–80 characters from `[A-Za-z0-9_-]`; UUIDs are suitable. Capture IDs are server-generated 32-character lowercase hex. Times are UTC Unix seconds. Checksum values are standard padded base64 SHA-256.

| Method and route | Body / response |
| --- | --- |
| GET `/onboarding/id/document-policy` | Enabled single document type, purpose/jurisdiction, `required_slots`, limits, policy version and retention |
| POST `/onboarding/id/captures` | `{operation_key, document_type}` → capture |
| GET `/onboarding/id/status` | `{capture: record-or-null}`; current account selection |
| GET `/onboarding/id/captures/{id}` | capture |
| POST `/onboarding/id/captures/{id}/uploads` | `{operation_key, expected_revision, slot, sha256, size}` → `{capture, upload_id, url, headers, expires_in}` |
| POST `/onboarding/id/captures/{id}/finalize` | `{operation_key, expected_revision, upload_ids: {front, back}}` → 202 capture |
| POST `/onboarding/id/captures/{id}/retry-finalization` | `{operation_key}` → 202 capture |
| POST `/onboarding/id/captures/{id}/cancel` | `{}` → capture; deletion is asynchronous |

A capture contains `capture_id`, `evidence_version`, `revision`, `policy_version`, `document_type`, `state`, `expires_at`, `selected_uploads`, safe asset metadata and, after ready, `delete_after`. Bucket keys, VersionIds, owners, work intents and operation keys are never returned to Android. Upload instructions are PUT-only. The client sends the returned content-type, checksum and encryption headers, its exact declared Content-Length (set automatically by Android), JPEG body, and **no API bearer token**. Do not follow upload redirects or log the URL.

JPEG originals are limited to 4 MiB and 20 MP; decoded derivatives are re-encoded at quality 90 with a 4 MiB output limit. `jpeg-raster-v1` preserves the decoded raster and strips metadata. Android must orient pixels before upload; EXIF rotation is not applied by the worker. Both originals and processing assets have pinned VersionIds/checksums. A late signed PUT creates another version and cannot change the frozen manifest. Finalizing evidence cannot become ready after the 24-hour session deadline. A ten-day current/noncurrent lifecycle is an orphan safety net beyond the maximum eight-day lifetime of pinned evidence; the normal sweeper deletes earlier.

New upload operations select a fresh key and increment the revision. Replay/renewal uses the same key, checksum, size and side, and only works while that upload remains selected in an unexpired uploading capture. Finalization checks the exact selected slots, HEAD metadata and versioning, then freezes them with a conditional write. Replays return the existing operation; changed selections conflict. Capture creation is limited to five per account per UTC day, with 20 slot attempts per capture. A record permits three automatic validation attempts; an authenticated manual retry of a failed record reuses the frozen evidence before the 24-hour session deadline.

States: `uploading`, `finalizing`, `ready`, `requires_recapture`, `failed`, `cancelled`, `expired`. Ready means safely decoded files only. Missing/unsafe files cannot reach ready. Safe errors: 400 invalid request, 401 sign-in, 404 absent/non-owned, 409 revision conflict/incomplete uploads, 410 expiry, 422 invalid image, 429 capture budget, 503 disabled/unavailable. Reconcile with status after uncertain responses. Do not blindly create another session.

## Deployment and operations

`build.sh` / `build.bat` package all three new Lambdas. `infra/capture.go` provisions a private versioned, owner-enforced SSE-S3 bucket; TLS-only policy; encrypted DynamoDB with PITR and a `work-due` GSI; encrypted SQS + DLQ; worker mapping; minute sweeper; JWT routes; scoped roles; and a DLQ alarm. Capture reservations and worker queue concurrency are [configured per stack](github-deployment.md#capture-concurrency-per-environment); dev uses shared capacity and a queue maximum of two. No resources were deployed during implementation.

Capture defaults **disabled**. Before enabling, choose and evaluate a two-sided document type and jurisdiction, approve the purpose and seven-day image retention, verify the AWS region, connect alarm notifications, and complete the real-device/S3 smoke checks below. Set Pulumi `captureDocumentType`, `captureJurisdiction`, `capturePurpose` and then `captureEnabled=true`. `sample_card` cannot be enabled in live infrastructure. This release supports exactly `front`/`back`; passports and additional policies require another implementation/policy version. SSE-KMS is not configured by this release.

Unfinished sessions expire after 24 hours; URLs last five minutes. Expiry/cancellation/unsafe-image cleanup waits six minutes to cover outstanding upload URLs and the 90-second worker lifetime. Ready images expire seven days after validation. Cleanup deletes **all original and processed versions and delete markers**. Late requests completing after a cleanup sweep are covered by the ten-day S3 current/noncurrent lifecycle; that fallback is not an exact-time deletion guarantee. Pending cleanup rows have no TTL and remain sweepable until deletion succeeds. Only completed rows/operation mappings and historical ready events are eligible for 90-day metadata TTL. Backups and PITR follow their separate configured retention; no deletion promise extends to provider backups implicitly.

The API role needs `GetObject` for HEAD metadata (S3 has no HEAD-only IAM action); no download route exists. Worker and dispatcher have no Cognito rights. Parsing must receive its own scoped role later. Capture metadata and queue messages contain the Cognito subject; logs contain neither it nor image data, URLs, tokens, provider diagnostics or document fields.

## Verification

Unit tests cover create replay/ownership, immutable version reads, stale finalize rejection, cancellation during work, invalid JPEGs, expiration/deletion, strict request bodies, JWT context, signed headers, bounded reads, XML version deletion, and DynamoDB ID round trips. Run `go test -race ./...`, `go vet ./...`, `bash build.sh`, `python3 scripts/check_lambda_archives.py`, and `(cd infra && go build ./...)`.

Deployment smoke gate: real Cognito access vs ID/expired tokens; cross-account IDs; real PUT with exact/changed checksum/type; missing side; late overwrites after finalize; duplicate SQS deliveries; terminate a leased worker; cancel during validation; wait for URL expiry and check all version deletion; DLQ/sweep recovery; inspect pinned metadata-free derivatives and ready event. No AWS integration result is implied by unit tests.
