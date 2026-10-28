# ID capture

Implementation of the [onboarding capture design](https://github.com/lambdawalker/design.attestra/tree/main/auth/onboarding/id-capture). Capture extracts editable document fields with Gemma 4 E2B on Amazon Bedrock. It does not verify authenticity, perform face matching, or authorize restricted features.

## Components

- `capture`: account-scoped state machine, upload/finalize idempotency, revision fences, bounded file checks and cleanup.
- `captureapi`: API Gateway HTTP v2 handler. Every route requires the Cognito JWT authorizer; the handler additionally requires `token_use=access` and `sub` from the verified authorizer context. Client-provided owners, keys and URLs are never accepted.
- `awscapture`: DynamoDB conditional writes and transactional ready outbox, private S3 SigV4 REST adapter, SQS dispatch using the existing AWS credential provider/signer. No new SDK modules or permanent credentials.
- `capture-worker`: version-specific reads, SHA-256/length checks, JPEG decoding with a 20 MP limit, metadata-free JPEG derivatives, ready publication and deletion.
- `capture-dispatcher`: each minute queries the durable work index and sends SQS jobs. Delivery is at least once; revision/generation/120-second leases prevent stale publication. The Lambda timeout is 90 seconds.

Durable validation/cleanup intent is stored **on the capture record** with its state transition, so no transaction can leave a frozen capture without work. This replaces a separate validation-outbox row with a co-located outbox. Ready publication uses a DynamoDB transaction containing the capture and an immutable `READY#...` event. A separate extraction phase runs from durable `extract` work on the capture worker. It rechecks the owned capture's current state, evidence version and retention before reading the pinned `Processed` manifest; historical events do not override cancellation/expiry.

## HTTP contract

All routes use `Authorization: Bearer <Cognito access token>`. JSON responses have `Cache-Control: no-store`. Operation keys are 16–80 characters from `[A-Za-z0-9_-]`; UUIDs are suitable. Capture IDs are server-generated 32-character lowercase hex. Times are UTC Unix seconds. Checksum values are standard padded base64 SHA-256.

| Method and route | Body / response |
| --- | --- |
| GET `/onboarding/id/document-policy` | Enabled single document type, purpose/jurisdiction, `required_slots`, limits, policy version and retention |
| POST `/onboarding/id/captures` | `{operation_key, document_type, expected_policy_version}` → capture |
| GET `/onboarding/id/status` | `{capture: record-or-null}`; current account selection |
| GET `/onboarding/id/captures/{id}` | capture |
| POST `/onboarding/id/captures/{id}/uploads` | `{operation_key, expected_revision, slot, sha256, size}` → `{capture, upload_id, url, headers, expires_in}` |
| POST `/onboarding/id/captures/{id}/finalize` | `{operation_key, expected_revision, upload_ids: {front, back}}` → 202 capture |
| POST `/onboarding/id/captures/{id}/retry-finalization` | `{operation_key}` → 202 capture |
| POST `/onboarding/id/captures/{id}/review` | `{expected_revision, data}` → capture with separate `reviewed` data |
| POST `/onboarding/id/captures/{id}/cancel` | `{}` → capture; deletion is asynchronous |

New creation binds the reviewed content-sensitive policy identity through `expected_policy_version`; mismatches return `409 policy_changed` and require refreshed disclosure and another explicit start. The exact policy snapshot is persisted and returned as `policy` on new records. Existing-key replay returns that original snapshot even after a policy change or disablement. Legacy records omit `policy`; do not fabricate their old disclosure.

A capture contains `capture_id`, `evidence_version`, `revision`, `policy_version`, `document_type`, `state`, `expires_at`, `selected_uploads`, safe asset metadata and, after ready, `delete_after`. Bucket keys, VersionIds, owners, work intents and operation keys are never returned to Android. Upload instructions are PUT-only. The client sends the returned content-type, checksum and encryption headers, its exact declared Content-Length (set automatically by Android), JPEG body, and **no API bearer token**. Do not follow upload redirects or log the URL.

JPEG originals are limited to 4 MiB and 20 MP; decoded derivatives are re-encoded at quality 90 with a 4 MiB output limit. `jpeg-raster-v1` preserves the decoded raster and strips metadata. Android must orient pixels before upload; EXIF rotation is not applied by the worker. Both originals and processing assets have pinned VersionIds/checksums. A late signed PUT creates another version and cannot change the frozen manifest. Finalizing evidence cannot become ready after the 24-hour session deadline. A ten-day current/noncurrent lifecycle is an orphan safety net beyond the maximum eight-day lifetime of pinned evidence; the normal sweeper deletes earlier.

New upload operations select a fresh key and increment the revision. Replay/renewal uses the same key, checksum, size and side, and only works while that upload remains selected in an unexpired uploading capture. Finalization checks the exact selected slots, HEAD metadata and versioning, then freezes them with a conditional write. Replays return the existing operation; changed selections conflict. Capture creation is limited to five per account per UTC day, with 20 slot attempts per capture. A record permits three automatic validation attempts and three automatic extraction attempts; an authenticated manual retry of a failed record reuses the frozen evidence before the 24-hour session deadline.

States: `uploading`, `finalizing`, `ready`, `requires_recapture`, `failed`, `cancelled`, `expired`. Ready means safely decoded files; check `extraction.status` separately before presenting fields. Missing/unsafe files cannot reach ready. Safe errors: 400 invalid request, 401 sign-in, 404 absent/non-owned, 409 policy changed/revision conflict/incomplete uploads, 410 expiry, 422 invalid image, 429 capture budget, 503 disabled/unavailable. Reconcile with status after uncertain responses. Do not blindly create another session.

## Deployment and operations

`scripts/build.sh` / `scripts/build.bat` package all three new Lambdas. `infra/capture.go` provisions a private versioned, owner-enforced SSE-S3 bucket; TLS-only policy; encrypted DynamoDB with PITR and a `work-due` GSI; encrypted SQS + DLQ; worker mapping; minute sweeper; JWT routes; scoped roles; and a DLQ alarm. Capture reservations and worker queue concurrency are [configured per stack](github-deployment.md#capture-concurrency-per-environment); dev uses shared capacity and a queue maximum of two. No resources were deployed during implementation.

Capture defaults **disabled**. Before enabling, choose and evaluate a two-sided document type and jurisdiction, approve the purpose and seven-day image retention, verify the AWS region, connect alarm notifications, and complete the real-device/S3 smoke checks below. Publish the approved settings through the [admin console](admin-runtime-config.md). Pulumi `captureDocumentType`, `captureJurisdiction`, `capturePurpose` and `captureEnabled` provide defaults until an admin override; fenced synchronization preserves overrides. `sample_card` cannot be enabled in live infrastructure. This release supports exactly `front`/`back`; passports and additional policies require another implementation/policy version. SSE-KMS is not configured by this release.

Unfinished sessions expire after 24 hours; URLs last five minutes. Expiry/cancellation/unsafe-image cleanup waits six minutes to cover outstanding upload URLs and the 90-second worker lifetime. Ready images expire seven days after validation. Cleanup deletes **all original and processed versions and delete markers**. Late requests completing after a cleanup sweep are covered by the ten-day S3 current/noncurrent lifecycle; that fallback is not an exact-time deletion guarantee. Pending cleanup rows have no TTL and remain sweepable until deletion succeeds. Only completed rows/operation mappings and historical ready events are eligible for 90-day metadata TTL. Backups and PITR follow their separate configured retention; no deletion promise extends to provider backups implicitly.

The API role needs `GetObject` for HEAD metadata (S3 has no HEAD-only IAM action); no download route exists. Worker and dispatcher have no Cognito rights. Only the worker receives `bedrock-mantle:CreateInference`, restricted to `google.gemma-4-e2b` by the model condition; API and dispatcher roles cannot invoke models. The worker can read pinned processed versions. Capture metadata and queue messages contain the Cognito subject; logs contain neither it nor image data, URLs, tokens, provider diagnostics or document fields.

## Verification

Unit tests cover create replay/ownership, immutable version reads, stale finalize rejection, cancellation during work, invalid JPEGs, expiration/deletion, strict request bodies, JWT context, signed headers, bounded reads, XML version deletion, and DynamoDB ID round trips. Run `go test -race ./...`, `go vet ./...`, `bash scripts/build.sh`, `python3 scripts/check_lambda_archives.py`, and `(cd infra && go build ./...)`.

Deployment smoke gate: real Cognito access vs ID/expired tokens; cross-account IDs; real PUT with exact/changed checksum/type; missing side; late overwrites after finalize; duplicate SQS deliveries; terminate a leased worker; cancel during validation; wait for URL expiry and check all version deletion; DLQ/sweep recovery; inspect pinned metadata-free derivatives and ready event. No AWS integration result is implied by unit tests.

## Card extraction and review

New captures automatically schedule extraction after file checks. `state=ready` remains compatible with older clients; `extraction.status` is `pending`, `complete`, or `failed`. Pending work is swept each minute and Android polls status. Provider failures retry up to three times with bounded backoff; failure preserves validated images. `retry-finalization` also retries failed extraction while the evidence is retained, without re-uploading. Historical ready captures without an `extraction` property are not silently backfilled.

`extraction.data` has string fields `full_name`, `given_names`, `surnames`, `birth_date`, `document_number`, `personal_number`, `address`, `issue_date`, `expiry_date`, and `issuing_country`. Empty strings mean absent/unreadable. Dates stay as printed, and numbers preserve leading zeros. These are unverified suggestions; the model is instructed not to follow text embedded in images or invent missing values. No model-generated confidence score is presented as reliable evidence.

The authenticated review endpoint accepts at most 512 UTF-8 bytes per field and requires nonempty data, a complete extraction and the current revision. It stores `reviewed: {data, saved_at}` separately, leaving extraction unchanged. Identical retries reconcile lost responses; conflicting edits return 409. This saves data for the next onboarding step but does not complete verification or unlock features. Form drafts are memory-only until saved.

Extraction uses the in-region Bedrock Mantle endpoint `/openai/v1/chat/completions`, model `google.gemma-4-e2b`, AWS SigV4 with the worker role, and no permanent API key. The adapter bounds image dimensions (1600 pixels on the longest side, with smaller fallbacks), JPEG size (1.2 MB each), total request (3.5 MB), output tokens (4096), response bytes (64 KiB), and inference time (55 seconds). Originals and normalized evidence remain unchanged. No image content, provider response, or extracted field enters logs. Cleanup erases extracted and reviewed data as well as images; API reads suppress the fields immediately after cancellation or expiry. Immutable ready events contain no extracted or reviewed PII.

Deploy the **backend** before releasing the updated Android app; shared index/admin infrastructure does not change. Confirm model access and quota in the deployed AWS region (Ohio is supported), then run a live sample-card extraction/review smoke test. Account-level Bedrock access, provider retention settings and real-card accuracy cannot be proven by unit tests. No live inference or AWS deployment was performed during development. See [AWS model documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-google-gemma-4-e2b.html).
