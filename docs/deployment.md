# Local deployment with Go and S3 state

The Go tool in `tools/deploy` provides terminal forms using [Charm Huh](https://github.com/charmbracelet/huh). It runs on Windows, Linux and macOS. The UI dependencies live in a separate Go module and do not enter Lambda binaries.

## Prerequisites

- Go 1.26.6+, AWS CLI v2 and Pulumi on PATH. Migration requires Pulumi CLI 3.254.0+ (`pulumi version`).
- Windows PowerShell on Windows; Bash and zip on Linux/macOS for the repository's build scripts.
- AWS credentials authorized to deploy the stack and access the state bucket.
- An existing private S3 state bucket in the same AWS account and region as the application stack.

In the AWS S3 console, create a **separate state bucket**, with a globally unique name. Keep all four Block Public Access options enabled, enable bucket versioning, and retain default server-side encryption. Use `us-east-2` for the current `dev` stack. Do not put this bucket in the application stack it stores, or apply ID-evidence expiration rules to it. The tool verifies ownership, versioning and the bucket-level public access block before continuing; it does not create or modify buckets.

The deployment credentials need `s3:ListBucket`, `s3:GetObject`, `s3:PutObject`, and `s3:DeleteObject` for state/locks/history, plus `s3:GetBucketVersioning` and `s3:GetBucketPublicAccessBlock` for preflight checks. Restrict object permissions to the selected prefix when using one. AWS infrastructure permissions are still required separately. A bucket using SSE-KMS additionally needs the applicable KMS permissions.

## AWS credentials

By default, the tool prompts using hidden input:

1. AWS access key ID.
2. AWS secret access key.
3. AWS session token, required for temporary credentials. A session token alone is insufficient. Leave it blank only for long-lived IAM access keys.
4. Pulumi passphrase for encrypting the stack's secrets.

Copy temporary credentials from your AWS credential source. Credentials are passed to AWS/Pulumi subprocesses through their environment, never as arguments or saved profiles. Existing AWS environment settings and shared profile files are isolated to avoid accidentally using another identity. The tool displays the account and ARN and asks you to confirm them. It does not refresh expired credentials; obtain a fresh set and rerun if they expire.

### AWS login alternative

Use AWS CLI's browser login instead of entering access keys:

```powershell
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Login -Profile attestra
```

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET -login -profile attestra
```

This requires a current AWS CLI v2 supporting `aws login` and `aws configure export-credentials`. The profile defaults to `default`; you can edit it in the prompt. The tool runs `aws login --profile PROFILE`, then captures temporary credentials using `aws configure export-credentials --format process`. Credential output is never displayed or written to a file by the deployment tool. AWS CLI itself updates the selected profile and caches the login session in its normal files.

The deployment uses a snapshot of the exported credentials, so rerun if they expire during a long deployment. Existing environment credentials are cleared for login; custom AWS config, shared-credentials and login-cache paths are honored. The same identity confirmation and isolated deployment credential handling apply. Add `-Login -Profile attestra` to the migration command below if desired. The Pulumi passphrase is still required.

Keep the Pulumi passphrase in a password manager. It is separate from AWS credentials. Losing it prevents decrypting the stack's secrets. It is not persisted by the tool or sent to Pulumi Cloud during normal deployment. Normal deployments require no Pulumi Cloud token. Local processes running as your user can still inspect process memory/environment; use a trusted machine.

## One-time migration from Pulumi Cloud

Pause deployments from other terminals and CI for the entire migration. This migrates the existing resource state; do **not** initialize an empty destination stack or delete deployed AWS resources.

From the repository root on Windows:

```powershell
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -MigrateFrom isdavid/attestra-auth-email/dev
```

Or on Linux/macOS:

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET -migrate-from isdavid/attestra-auth-email/dev
```

The tool asks you to enter and repeat a new passphrase and optionally enter a Pulumi Cloud access token **for this migration only**. You can leave that token blank if the Pulumi CLI already has a valid saved Cloud login. Access to the old backend is needed once to read/decrypt its existing state. The migration uses:

```text
pulumi stack migrate https://api.pulumi.com isdavid/attestra-auth-email/dev --target dev --secrets-provider passphrase
```

The S3 backend is selected through the subprocess's `PULUMI_BACKEND_URL`. Pulumi migrates resource state and re-encrypts configuration and state secrets using the new passphrase. It preserves the source backend state and backs up the original local configuration as `infra/Pulumi.dev.yaml.bak.*`. The tool removes `aws:profile` from the migrated configuration, verifies the target, then **stops without building or deploying**.

Review and commit the migrated `infra/Pulumi.dev.yaml` (encrypted values and encryption salt are safe to version). Retain the encrypted `.bak.*` file privately; Git ignores it. Then run a normal deployment and inspect the preview for unexpected replacements. A normal code change can add resources, but migration itself must not recreate existing infrastructure.

After successful migration, everyone must use the same S3 backend and passphrase. Do not deploy from the old Cloud stack: the two backend locks are independent. Keep the old state for recovery until the migrated setup is verified. The tool never removes it.

If migration fails, no deployment follows and nothing is rolled back automatically. Inspect both the S3 destination and local configuration before retrying. If state migrated but a later check failed, fix the indicated local configuration and use normal deployment; do not blindly migrate again. An existing destination is not overwritten by this tool.

## Deploy

```powershell
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET
.\deploy.ps1 -Stack dev -Backend s3://YOUR-STATE-BUCKET -Region us-east-2
# Optional: require clean Git status and pull the current branch first.
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Pull
```

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET
```

Omit `Backend` to enter the S3 URL in the form. A prefix such as `s3://YOUR-STATE-BUCKET/attestra` is supported. Use the exact same URL/prefix for migration and every deployment. The entered region must match `aws:region` in the stack.

The sequence is: prompt → verify AWS identity and bucket → optional Git pull → select existing S3 stack → check secrets provider/configuration → build Lambda archives → `pulumi preview` → `pulumi up`. Each failure stops subsequent steps. Pulumi retains its native deployment confirmation; no `--yes` flag is used. The build delegates to `build.ps1`/`build.sh`, including any capture Lambdas added there.

Git pull is opt-in so it does not prevent deploying reviewed local changes or interfere with migration edits. Commit/stash changes before choosing it. The launchers support paths containing spaces and forward failures. `go run` reports failures as a nonzero exit; its wrapper may report 1 rather than the underlying command's exact status.

For standalone Pulumi commands, explicitly select the S3 backend with `pulumi login s3://YOUR-STATE-BUCKET` and supply the same AWS credentials/passphrase in your shell. The deployment tool sets these only for its own subprocesses; it does not change your shell's login or persist secrets.

## Verification

```bash
go -C tools/deploy test ./...
go -C tools/deploy vet ./...
```

CI runs these on Linux and Windows and checks the Windows launchers with a stub Go executable. Tests cover failure short-circuiting, migration without deployment, credential isolation and configuration guards. They do not contact AWS or migrate real state.
