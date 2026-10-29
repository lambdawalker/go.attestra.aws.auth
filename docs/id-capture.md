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
| GET `/onboarding/id/document-policy` | Enabled ID-card policy, localized `purposes`, `required_slots`, limits, policy version and retention |
| POST `/onboarding/id/captures` | `{operation_key, document_type, expected_policy_version, disclosure_language}` → capture |
| GET `/onboarding/id/status` | `{capture: record-or-null}`; current account selection |
| GET `/onboarding/id/captures/{id}` | capture |
| POST `/onboarding/id/captures/{id}/uploads` | `{operation_key, expected_revision, slot, sha256, size}` → `{capture, upload_id, url, headers, expires_in}` |
| POST `/onboarding/id/captures/{id}/finalize` | `{operation_key, expected_revision, upload_ids: {front, back}}` → 202 capture |
| POST `/onboarding/id/captures/{id}/retry-finalization` | `{operation_key}` → 202 capture |
| POST `/onboarding/id/captures/{id}/review` | `{expected_revision, data, origin_country}` → capture with separate `reviewed` data |
| POST `/onboarding/id/captures/{id}/cancel` | `{}` → capture; deletion is asynchronous |

New creation binds the reviewed content-sensitive policy identity through `expected_policy_version`; mismatches return `409 policy_changed` and require refreshed disclosure and another explicit start. The exact policy snapshot is persisted and returned as `policy` on new records. Existing-key replay returns that original snapshot even after a policy change or disablement. Legacy records omit `policy`; do not fabricate their old disclosure.

A capture contains `capture_id`, `evidence_version`, `revision`, `policy_version`, `document_type`, `state`, `expires_at`, `selected_uploads`, safe asset metadata and, after ready, `delete_after`. Bucket keys, VersionIds, owners, work intents and operation keys are never returned to Android. Upload instructions are PUT-only. The client sends the returned content-type, checksum and encryption headers, its exact declared Content-Length (set automatically by Android), JPEG body, and **no API bearer token**. Do not follow upload redirects or log the URL.

JPEG originals are limited to 4 MiB and 20 MP; decoded derivatives are re-encoded at quality 90 with a 4 MiB output limit. `jpeg-raster-v1` preserves the decoded raster and strips metadata. Android must orient pixels before upload; EXIF rotation is not applied by the worker. Both originals and processing assets have pinned VersionIds/checksums. A late signed PUT creates another version and cannot change the frozen manifest. Finalizing evidence cannot become ready after the 24-hour session deadline. A ten-day current/noncurrent lifecycle is an orphan safety net beyond the maximum eight-day lifetime of pinned evidence; the normal sweeper deletes earlier.

New upload operations select a fresh key and increment the revision. Replay/renewal uses the same key, checksum, size and side, and only works while that upload remains selected in an unexpired uploading capture. Finalization checks the exact selected slots, HEAD metadata and versioning, then freezes them with a conditional write. Replays return the existing operation; changed selections conflict. Capture creation is limited to five per account per UTC day, with 20 slot attempts per capture. A record permits three automatic validation attempts and three automatic extraction attempts; an authenticated manual retry of a failed record reuses the frozen evidence before the 24-hour session deadline.

States: `uploading`, `finalizing`, `ready`, `requires_recapture`, `failed`, `cancelled`, `expired`. Ready means safely decoded files; check `extraction.status` separately before presenting fields. Missing/unsafe files cannot reach ready. Safe errors: 400 invalid request, 401 sign-in, 404 absent/non-owned, 409 policy changed/revision conflict/incomplete uploads, 410 expiry, 422 invalid image, 429 capture budget, 503 disabled/unavailable. Reconcile with status after uncertain responses. Do not blindly create another session.

## Deployment and operations

`scripts/build.sh` / `scripts/build.bat` package all three new Lambdas. `infra/capture.go` provisions a private versioned, owner-enforced SSE-S3 bucket; TLS-only policy; encrypted DynamoDB with PITR and a `work-due` GSI; encrypted SQS + DLQ; worker mapping; minute sweeper; JWT routes; scoped roles; and a DLQ alarm. Capture reservations and worker queue concurrency are [configured per stack](github-deployment.md#capture-concurrency-per-environment); dev uses shared capacity and a queue maximum of two. No resources were deployed during implementation.

Capture defaults **disabled**. The onboarding document category is `id_card`, including driver’s licenses and state/federal IDs. Publish the generic disclosure in English, Spanish and French through the [admin console](admin-runtime-config.md); no country/language matrix or pre-scan jurisdiction is required. `captureEnabled` supplies the deployment default until an admin override. Old document-type, jurisdiction and purpose Pulumi settings no longer override the generic defaults. Review seven-day retention, AWS region, alarms and the device/S3 checks before enabling capture. This release supports exactly `front`/`back`; passports require a different capture flow. SSE-KMS is not configured by this release.

Unfinished sessions expire after 24 hours; URLs last five minutes. Expiry/cancellation/unsafe-image cleanup waits six minutes to cover outstanding upload URLs and the 90-second worker lifetime. Ready images expire seven days after validation. Cleanup deletes **all original and processed versions and delete markers**. Late requests completing after a cleanup sweep are covered by the ten-day S3 current/noncurrent lifecycle; that fallback is not an exact-time deletion guarantee. Pending cleanup rows have no TTL and remain sweepable until deletion succeeds. Only completed rows/operation mappings and historical ready events are eligible for 90-day metadata TTL. Backups and PITR follow their separate configured retention; no deletion promise extends to provider backups implicitly.

The API role needs `GetObject` for HEAD metadata (S3 has no HEAD-only IAM action); no download route exists. Worker and dispatcher have no Cognito rights. Only the worker receives `bedrock-mantle:CreateInference`, restricted to `google.gemma-4-e2b` by the model condition; API and dispatcher roles cannot invoke models. The worker can read pinned processed versions. Capture metadata and queue messages contain the Cognito subject; logs contain neither it nor image data, URLs, tokens, provider diagnostics or document fields.

## Verification

Unit tests cover create replay/ownership, immutable version reads, stale finalize rejection, cancellation during work, invalid JPEGs, expiration/deletion, strict request bodies, JWT context, signed headers, bounded reads, XML version deletion, and DynamoDB ID round trips. Run `go test -race ./...`, `go vet ./...`, `bash scripts/build.sh`, `python3 scripts/check_lambda_archives.py`, and `(cd infra && go build ./...)`.

Deployment smoke gate: real Cognito access vs ID/expired tokens; cross-account IDs; real PUT with exact/changed checksum/type; missing side; late overwrites after finalize; duplicate SQS deliveries; terminate a leased worker; cancel during validation; wait for URL expiry and check all version deletion; DLQ/sweep recovery; inspect pinned metadata-free derivatives and ready event. No AWS integration result is implied by unit tests.

## Card extraction and review

New captures automatically schedule extraction after file checks. `state=ready` remains compatible with older clients; `extraction.status` is `pending`, `complete`, or `failed`. Pending work is swept each minute and Android polls status. Provider failures retry up to three times with bounded backoff; failure preserves validated images. `retry-finalization` also retries failed extraction while the evidence is retained, without re-uploading. Historical ready captures without an `extraction` property are not silently backfilled.

`extraction.data` has string fields `full_name`, `given_names`, `surnames`, `birth_date`, `document_number`, `personal_number`, `address`, `issue_date`, `expiry_date`, and `issuing_country`. Empty strings mean absent/unreadable. Dates stay as printed, and numbers preserve leading zeros. These are unverified suggestions; the model is instructed not to follow text embedded in images or invent missing values. No model-generated confidence score is presented as reliable evidence.

The authenticated review endpoint accepts at most 512 UTF-8 bytes per field and requires nonempty data, a ready capture with either complete or failed extraction, and the current revision. It stores `reviewed: {data, saved_at, origin_country}` separately, leaving extraction unchanged. Identical retries reconcile lost responses; conflicting edits return 409. This saves data for the next onboarding step but does not complete verification or unlock features. Form drafts are memory-only until saved. Failed extraction may be followed by manual entry without re-uploading images or rerunning extraction. Manual review remains separate from extraction and leaves its failed status unchanged; pending extraction, unvalidated files, expired evidence and cancelled captures cannot be reviewed. Deploy this backend change before the Android direct-capture/manual-entry flow.

Extraction uses the in-region Bedrock Mantle endpoint `/openai/v1/chat/completions`, model `google.gemma-4-e2b`, AWS SigV4 with the worker role, and no permanent API key. The adapter bounds image dimensions (1600 pixels on the longest side, with smaller fallbacks), JPEG size (1.2 MB each), total request (3.5 MB), output tokens (4096), response bytes (64 KiB), and inference time (55 seconds). Originals and normalized evidence remain unchanged. No image content, provider response, or extracted field enters logs. Cleanup erases extracted and reviewed data as well as images; API reads suppress the fields immediately after cancellation or expiry. Immutable ready events contain no extracted or reviewed PII.

Deploy the **backend** before releasing the updated Android app; shared index/admin infrastructure does not change. Confirm model access and quota in the deployed AWS region (Ohio is supported), then run a live sample-card extraction/review smoke test. Account-level Bedrock access, provider retention settings and real-card accuracy cannot be proven by unit tests. No live inference or AWS deployment was performed during development. See [AWS model documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-google-gemma-4-e2b.html).

## Disclosure and country of origin

The client selects `en`, `es` or `fr` from policy `purposes`, falling back to English when unavailable. Creation records `disclosure_language` together with the immutable policy snapshot. Retries must retain that language and expected policy version; changing either conflicts. Changing a translation changes policy identity and requires renewed disclosure for new captures. Legacy records do not invent a historical language or snapshot.

The post-scan review request accepts `origin_country`: `CA`, `US`, `MX`, `GT` or `BZ`. Android requires the user to select it before saving. It is separate from OCR `issuing_country` and is stored in `reviewed.origin_country`. The backend accepts omission for older clients; neither OCR nor locale establishes origin. The form follows document submission and remains editable before confirmation.

## Durable onboarding capture fact

The ready transition atomically sets permanent account `IDCaptured` and increments the account revision. `/auth/status` can therefore report `id_captured` after evidence retention expires, without requiring the client to have polled status before expiry. Legacy ready records are backfilled when discovered; expired pre-upgrade records cannot be reconstructed. This is evidence-capture completion, never identity verification. See [onboarding policy](onboarding-policy.md).

## Client save and finish

The final form is already backed by `POST /onboarding/id/captures/{id}/review`. Send the current capture revision as `expected_revision`, edited `data`, and the Android-required `origin_country` (older clients may omit origin). The successful response includes the stored `reviewed` object (`data`, `saved_at`, `origin_country`) and updated capture revision. The service persists it through a conditional record update before returning. It does not write these values into Cognito identity attributes or mark identity verified.

Clients may show **Details saved** only after receiving that stored review, or reconciling an uncertain response through GET/current and finding the exact submitted fields and origin. Keep unsaved edits on validation, authentication, transport and revision errors. An identical retry is safe; a stale different edit must reload/reconcile before retrying. A saved review can be reopened and corrected with the latest revision. Extraction failure remains recorded even after a successful manual review.

`id_captured` and policy-based `onboarding_complete` are distinct from reviewed-data persistence: file readiness can satisfy capture enrollment before review. Do not use either flag as the final form's save acknowledgement. The finish screen is a client transition, with no new API route or identity-validation decision.

Android automatically uploads/finalizes the confirmed front/back pair, displays loading during pending extraction, and opens populated review or empty manual entry after terminal extraction. Deploy extraction/review support, including failed-extraction review, before releasing that client. See the [Android flow](https://github.com/lambdawalker/android.attestra.auth/blob/main/docs/identity-capture.md) and [central design](https://github.com/lambdawalker/design.attestra/blob/main/auth/onboarding/id-parsing/flow.md).
