# Local deployment with Go and S3 state

The Go tool in `tools/deploy` provides terminal forms using [Charm Huh](https://github.com/charmbracelet/huh). It runs on Windows, Linux and macOS. The UI dependencies live in a separate Go module and do not enter Lambda binaries. The [tool package map](../tools/deploy/README.md) describes the internal code organization and where to add tests.

For a deployment from scratch, start with the [first-deployment guide](first-deployment.md). The routine deploy tool expects an existing bucket and stack; the `setup.bat` / `setup.sh` wizard can create them. migration below is only for existing Pulumi Cloud stacks.

## Prerequisites

- Go 1.26.6+, AWS CLI v2 and Pulumi on PATH. Migration requires Pulumi CLI 3.254.0+ (`pulumi version`).
- Direct `.bat` launchers on Windows or a POSIX shell for `.sh` launchers. All build/deployment logic is Go; no PowerShell or external ZIP utility is needed.
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

Copy temporary credentials from your AWS credential source. Credentials are passed to AWS/Pulumi subprocesses through their environment, never as arguments or saved profiles. Existing AWS environment settings and shared profile files are isolated to avoid accidentally using another identity. The tool displays the account and ARN and asks you to confirm them. Manually pasted credentials cannot renew automatically; obtain a fresh set and rerun if they expire.

### IAM Identity Center (SSO)

For an existing SSO profile such as `attestra`, use:

```powershell
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Sso -Profile attestra
# First migration:
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Sso -Profile attestra -MigrateFrom isdavid/attestra-auth-email/dev
```

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET -sso -profile attestra
```

`-Sso` runs `aws sso login --profile attestra` and then uses a refreshable credential provider without displaying credentials. The SSO region comes from your existing profile/session; it can differ from the deployment region. Your SSO configuration is preserved. `-Sso` works alone; when combined with `-Login`, SSO takes precedence. Login failures stop deployment and retain the AWS diagnostic rather than assuming the CLI is outdated.

### AWS login alternative

Use AWS CLI's browser login instead of entering access keys:

```powershell
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Login -Profile attestra
```

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET -login -profile attestra
```

This requires a current AWS CLI v2 supporting `aws login` and `aws configure export-credentials`. The profile defaults to `default`; you can edit it in the prompt. The tool runs `aws login --profile PROFILE`, then supplies a private `credential_process` profile that calls `aws configure export-credentials --format process` whenever credentials need renewal. Credential output is never displayed or written to a file by the deployment tool. AWS CLI itself updates the selected profile and caches the login session in its normal files.

Pulumi (including S3 state), AWS CLI commands, and index publication can renew credentials from the selected profile during deployment. Automatic renewal lasts only as long as the underlying login session allows. For SSO, use a modern `sso-session` profile (configure with `aws configure sso --profile attestra`); legacy profiles may require signing in again. If renewal fails, sign in again and rerun setup to resume. A private temporary configuration contains only the helper command and profile locations; it is removed when the tool exits. The selected profile and AWS CLI session cache remain managed by AWS CLI. Existing environment credentials are cleared for login; custom AWS config, shared-credentials and login-cache paths are honored. The same identity confirmation and isolated deployment credential handling apply. Add `-Login -Profile attestra` to the migration command below if desired. The Pulumi passphrase is still required.

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
.\deploy.bat -Stack dev -Backend s3://YOUR-STATE-BUCKET -Region us-east-2
# Optional: require clean Git status and pull the current branch first.
.\deploy.bat -Backend s3://YOUR-STATE-BUCKET -Pull
```

```bash
go -C tools/deploy run . -backend s3://YOUR-STATE-BUCKET
```

Omit `Backend` to enter the S3 URL in the form. A prefix such as `s3://YOUR-STATE-BUCKET/attestra` is supported. Use the exact same URL/prefix for migration and every deployment. The entered region must match `aws:region` in the stack.

The sequence is: prompt → verify AWS identity and bucket → optional Git pull → select existing S3 stack → check secrets provider/configuration → build Lambda archives → `pulumi preview` → `pulumi up`. Each failure stops subsequent steps. Pulumi retains its native deployment confirmation; no `--yes` flag is used. The build invokes the Go packager in `tools/deploy`, also available through `build.bat`/`build.sh`.

Git pull is opt-in so it does not prevent deploying reviewed local changes or interfere with migration edits. Commit/stash changes before choosing it. The launchers support paths containing spaces and forward failures. `go run` reports failures as a nonzero exit; its wrapper may report 1 rather than the underlying command's exact status.

For standalone Pulumi commands, explicitly select the S3 backend with `pulumi login s3://YOUR-STATE-BUCKET` and supply the same AWS credentials/passphrase in your shell. The deployment tool sets these only for its own subprocesses; it does not change your shell's login or persist secrets.

## GitHub Actions

For unattended deployment using AWS OIDC credentials and the same S3 stack, see [GitHub deployment setup](github-deployment.md). Local login and SSO remain available for interactive runs.

## Verification

```bash
go -C tools/deploy test ./...
go -C tools/deploy vet ./...
```

CI runs these on Linux and Windows and checks the Windows launchers with a stub Go executable. Tests cover failure short-circuiting, migration without deployment, credential isolation and configuration guards. They do not contact AWS or migrate real state.

## Deploy a specific environment locally

Run setup once for each target. Use its backend and stack explicitly for routine local deployments:

```text
.\deploy.bat -Stack qa -Backend s3://YOUR-QA-STATE-BUCKET -Region us-east-2 -Sso -Profile attestra
```

On Linux/macOS use `./deploy.sh -stack qa -backend s3://YOUR-QA-STATE-BUCKET -region us-east-2 -sso -profile attestra`. Local deployment defaults to `dev`; it does not choose a GitHub environment. For GitHub deployment, choose the matching environment from the workflow dropdown.


### Final environment health check

After a full local deployment or GitHub Actions deploy/publish, the tool runs a read-only health check. The setup wizard runs it after API DNS and index publication, before saving its completed-setup receipt. Partial prerequisite deployments, previews, migrations, and teardown do not run it.

Checks include required Pulumi outputs, the configured API hostname, SES identity and DKIM verification, the Cognito app client, API DNS/TLS and the auth-status Lambda response, CORS for the app origin, and the public index entry's configuration/hash. The Lambda probe sends an unauthenticated POST to `/auth/status` and requires its exact `401 sign_in_required` response; it does not create users or send mail. Requests have a 15-second timeout and do not follow redirects.

Each result is shown as PASS, FAIL, WARN, or INFO. A required failure stops successful completion and causes the deployment action to fail, but retains all deployed resources and published index data. DNS propagation can cause a temporary failure: correct the reported issue or wait, then rerun only the checks:

```powershell
.\deploy.bat -health-check -Backend s3://YOUR-STATE-BUCKET -Stack qa -Region us-east-2 -Sso -Profile attestra
```

```sh
./deploy.sh -health-check -backend s3://YOUR-STATE-BUCKET -stack qa -region us-east-2 -sso -profile attestra
```

This authenticates and reads the selected stack without building or deploying. The recovery menu also offers **Run environment health check**. A standalone successful check does not mark an interrupted setup complete; rerun setup to finish its remaining steps.

Disabled ID capture and an unconfigured optional index are warnings. These checks are not a full sign-in/email/upload test, do not establish SES production access, and do not validate website hosting or every Lambda/queue. The existing unchanged-commit shortcut remains an explicitly local receipt, not a live health check; use `-health-check` when you want current remote evidence.


### Environment capacity profiles

Each stack saves `attestra-auth-email:environmentClass` as `test` or `production`. `dev`, `qa`, and new custom test environments default to:

```yaml
config:
  attestra-auth-email:environmentClass: test
  attestra-auth-email:captureReservedConcurrency: "-1"
  attestra-auth-email:captureWorkerMaxConcurrency: "2"
```

Existing explicit YAML values are preserved. The `prod` stack always uses production rules. For a custom name such as `live`, select Production in setup or set `environmentClass: production`. Production prompts for explicit capacity, requires positive reservations and a worker maximum of 2–1000 no larger than the reservation, and checks quota before preview/update. A reservation is per function; there are three capture functions. Two workers means at most two simultaneous SQS worker invocations.

Insufficient production capacity stops deployment with required and available figures. Test environments retain the logged shared-capacity fallback. Direct production `pulumi` commands must be run through the deployment tool/GitHub action so the quota preflight runs. Quotas can still change after a preflight if another deployment changes reservations.

### Manage saved credentials without setup

Run `credentials.bat` on Windows or `./credentials.sh` on Unix (`deploy -manage-credentials` is equivalent). Select the environment, then replace GitHub/Cloudflare tokens, a manual AWS credential set, or a saved Pulumi passphrase; change the vault passphrase; or delete the environment's vault. Inputs are hidden. Replacement and passphrase rotation preserve other entries and use the existing encrypted-file replacement mechanism. Rotation requires the current passphrase; deletion requires typing the environment name and works even if the passphrase is lost.

These operations are local. They do not rotate/revoke provider tokens, alter GitHub secrets, change Pulumi stack encryption, or remove AWS CLI session caches. Replacing a saved Pulumi passphrase means saving the stack's current passphrase, not changing that stack's encryption password.

### Export Android configuration

Successful setup automatically writes `android-config/<environment>.properties` (ignored by Git). It contains only public settings: environment, API URL, app link host, region, Cognito pool/client IDs, capture enabled flag, and document type. Failed output reads leave any previous export intact. The capture document policy is still enforced by the server.

Export again without deployment:

```powershell
.\deploy.bat -export-android .\android-config\qa.properties -Backend s3://YOUR-STATE-BUCKET -Stack qa -Region us-east-2 -Sso -Profile attestra
```

The setup recovery menu also offers **Export Android configuration**. In `android.attestra.auth`, build using the generated file:

```powershell
.\gradlew.bat :app:assembleDebug -PattestraConfigFile=D:/dev/go.attestra.aws.auth/android-config/qa.properties
```

On Unix, use `./deploy.sh` and `./gradlew` with the same flags. Re-export and rebuild when switching environments; the file does not change an already installed app. No AWS access keys, tokens, or Pulumi secrets are exported.
