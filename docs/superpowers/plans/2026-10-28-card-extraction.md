# Card extraction and review

Implement the approved Gemma 4 E2B approach using AWS Bedrock Mantle and Lambda role credentials. Preserve the existing capture.ready meaning (validated, version-pinned images). Queue a separate extraction work item on the same durable record; status reports pending/complete/failed independently. Read only the two pinned normalized images. Bound image/request/output sizes, inference deadline and retries. Revision fences prevent cancellation, expiry or duplicate workers from publishing stale fields.

Use a fixed schema: full name, given names, surnames, birth date, document number, personal number (e.g. CURP), address, issue/expiry dates and issuing country. Missing/unreadable values stay empty. These are editable suggestions, never identity verification. Store user-reviewed values separately with revision concurrency control. Delete both at capture cleanup and suppress them immediately on cancellation/expiry. Do not copy PII to immutable ready events or logs.

Android polls the existing status, displays extraction progress/failure/retry, then an editable review form. Save reviewed fields through an authenticated capture endpoint. Keep draft PII in memory only; status refresh must not overwrite unsaved changes. Existing records without extraction remain readable and deletable.

Steps: backend contract/state machine and tests; bounded signed Bedrock adapter; infrastructure permissions/configuration/routes; Android models/controller/form and tests; integration docs, checks, checkpoint pushes and PRs. No AWS deployment as part of implementation. Live model accuracy, account access and device camera testing remain deployment smoke checks.
