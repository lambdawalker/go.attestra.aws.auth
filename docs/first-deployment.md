# First deployment from scratch

Use this guide when there is **no existing application stack** to preserve. The current workflow deploys `dev` from `main` in `us-east-2`. QA/prod concurrency can be configured per stack, but the setup wizard and workflow do not yet provision or select those environments.

If you already have a working deployment, skip to [routine deployments](#routine-deployments). If resources already exist under Pulumi Cloud, use [state migration](deployment.md#one-time-migration-from-pulumi-cloud) instead of creating an empty stack. A local `.bak` YAML file alone is not the resource state.

## What is automated?

| Step | Who handles it today? |
| --- | --- |
| AWS account, authorized local login, developer tools | You / AWS administrator |
| Private, versioned S3 state bucket | You, once |
| Pulumi stack initialization and application configuration | You, once |
| AWS GitHub OIDC provider, deployment IAM role and permissions | `setup-github.bat` |
| GitHub `dev` environment, variables and passphrase secret | `setup-github.bat` |
| Tests, Lambda archives, preview and infrastructure deployment | `Deploy AWS [Pulumi S3]` action |
| SES identity and DKIM token creation | Pulumi |
| Cloudflare DNS records and waiting for SES verification | You, for a new identity |
| Android API/Cognito configuration | You, after deployment |

The setup script expects the state bucket to exist. The deployment action expects an initialized stack. Neither creates the state bucket or initializes a missing stack, and neither manages Cloudflare DNS. Normal operation does not use the Pulumi Cloud API or need a Pulumi Cloud token. S3 storage and requests can incur AWS charges.

## 1. Prepare tools and AWS access

Install Git, Go 1.26.6+, a current AWS CLI v2, and Pulumi CLI (the workflow pins 3.264.0). Clone the repository and use `main`. Windows examples below assume `D:\dev\go.attestra.aws.auth`.

Your AWS identity needs permission to create the state bucket and bootstrap the deployment role/OIDC provider. The setup wizard cannot grant privileges to the identity running it. See [setup permissions](github-deployment.md#guided-environment-setup).

For IAM Identity Center, configure the profile once if necessary, then sign in:

```powershell
aws configure sso --profile attestra
aws sso login --profile attestra
aws sts get-caller-identity --profile attestra
```

Confirm the account ID is the intended account. For an already configured profile, omit `aws configure sso`. An SSO profile uses `aws sso login`; `aws login` is a separate browser login method and must use a suitable non-SSO profile. The setup wizard also supports entering an access key, secret key and, for temporary credentials, session token. A session token alone is not sufficient.

## 2. Create the S3 state bucket

In **S3 → Create bucket**, choose:

- A globally unique bucket name, in the intended AWS account and **us-east-2**.
- All four **Block Public Access** settings enabled.
- **Bucket versioning** enabled.
- Default server-side encryption (SSE-S3 is sufficient for this bootstrap).

This bucket stores Pulumi state, locks and history. Keep it separate from the application stack and the ID-evidence bucket. Do not apply evidence expiration rules to state. For an existing configured deployment, reuse its exact bucket and prefix; do not create a replacement.

The wizard's current default is `s3://pulumi-state-1p8322nx`. That is an existing deployment's bucket, not a reusable name for a new account. Supply your own bucket URL when bootstrapping elsewhere.

## 3. Initialize a fresh Pulumi stack

These instructions are **only for a new stack with no existing resources**. If `dev` already exists, select it and keep its configuration/passphrase; do not remove it to rerun these steps.

In the same PowerShell window, choose the intended credentials and backend:

```powershell
Set-Location D:\dev\go.attestra.aws.auth\infra
# Remove ambient key credentials so they do not override the chosen profile.
Remove-Item Env:AWS_ACCESS_KEY_ID, Env:AWS_SECRET_ACCESS_KEY, Env:AWS_SESSION_TOKEN -ErrorAction SilentlyContinue
$env:AWS_PROFILE = "attestra"
$env:AWS_REGION = "us-east-2"
$env:AWS_DEFAULT_REGION = "us-east-2"
$stateBucket = Read-Host "Your existing S3 state bucket name"
$env:PULUMI_BACKEND_URL = "s3://${stateBucket}?awssdk=v2&region=us-east-2"
pulumi login $env:PULUMI_BACKEND_URL
if ($LASTEXITCODE -ne 0) { throw "Backend login failed" }
```

A checkout contains `Pulumi.dev.yaml` for the existing deployment. For a **different, fresh deployment**, move that file to a private backup outside the repository before initialization; it contains encryption metadata belonging to another stack. Do not reuse its encrypted `proofKey` or salt. For an existing deployment, keep the file and skip initialization.

Run the following complete block. `Invoke-BootstrapCommand` is defined here; it is a helper function, not software to install. It stops on failed CLI commands. Replace the example application origin/domain/address for your deployment.

```powershell
function Invoke-BootstrapCommand {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    & pulumi @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Pulumi command failed; stop and inspect the error" }
}

# Choose a strong passphrase, save it in your password manager, and use the
# exact same value later in setup-github. Do not paste it into chat or Git.
Remove-Item Env:PULUMI_CONFIG_PASSPHRASE_FILE -ErrorAction SilentlyContinue
$securePassphrase = Read-Host "New dev stack passphrase" -AsSecureString
$passphrasePointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassphrase)
$keyBytes = New-Object byte[] 32
try {
    $env:PULUMI_CONFIG_PASSPHRASE = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passphrasePointer)
    if ([string]::IsNullOrEmpty($env:PULUMI_CONFIG_PASSPHRASE)) { throw "A nonempty passphrase is required" }
    Invoke-BootstrapCommand stack init dev --secrets-provider passphrase
    Invoke-BootstrapCommand config set aws:region us-east-2 --stack dev
    Invoke-BootstrapCommand config set attestra-auth-email:appOrigin https://attestrabond.com --stack dev
    Invoke-BootstrapCommand config set attestra-auth-email:senderDomain info.attestrabond.com --stack dev
    Invoke-BootstrapCommand config set attestra-auth-email:senderAddress verify@info.attestrabond.com --stack dev
    Invoke-BootstrapCommand config set --stack dev attestra-auth-email:captureReservedConcurrency -- -1
    Invoke-BootstrapCommand config set attestra-auth-email:captureWorkerMaxConcurrency 2 --stack dev

    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($keyBytes) } finally { $rng.Dispose() }
    $proofKey = [Convert]::ToBase64String($keyBytes)
    $proofKey | pulumi config set attestra-auth-email:proofKey --secret --stack dev
    if ($LASTEXITCODE -ne 0) { throw "Saving proofKey failed" }
} finally {
    Remove-Item Env:PULUMI_CONFIG_PASSPHRASE -ErrorAction SilentlyContinue
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passphrasePointer)
    $securePassphrase.Dispose()
    [Array]::Clear($keyBytes, 0, $keyBytes.Length)
    Remove-Variable proofKey -ErrorAction SilentlyContinue
}
```

The environment variable is necessary while piping `proofKey`: Pulumi cannot prompt for a passphrase through stdin when stdin contains the key. If key saving fails after stack initialization, **do not recreate the stack**. Supply the same passphrase again and resume the failed configuration step.

Review `Pulumi.dev.yaml`: it should contain `encryptionsalt` and encrypted `proofKey` (`secure: v1:...`). Do not set `aws:profile` in this file: GitHub uses temporary OIDC credentials. A local `AWS_PROFILE` environment variable is fine for manual commands. Keep `route53ZoneId` unset when DNS is hosted in Cloudflare.

Commit only the reviewed stack configuration, including its encrypted secret and salt:

```powershell
Set-Location D:\dev\go.attestra.aws.auth
git branch --show-current
git diff -- infra/Pulumi.dev.yaml
git add infra/Pulumi.dev.yaml
git commit -m "Configure dev stack with S3 state"
git push origin main
```

Confirm you are on `main` before committing. If push is rejected, reconcile changes; do not force-push. Never commit plaintext keys/passphrases, private backups or state exports.

## 4. Run GitHub/AWS setup

From the repository root:

```powershell
.\setup-github.bat
```

Follow the [guided setup](github-deployment.md#guided-environment-setup) for GitHub token permissions, AWS login choices and role creation. Use the same account, region, backend and stack initialized above. Enter the **same stack passphrase** for the GitHub environment secret `PULUMI_CONFIG_PASSPHRASE`.

The wizard creates or checks the OIDC provider, creates/updates its managed deployment role, and saves the verified `AWS_ROLE_ARN` with the GitHub environment settings. You do not need to construct the ARN or store AWS access keys in GitHub. Review the displayed permissions before applying them.

## 5. Preview and deploy

Open **Actions → Deploy AWS [Pulumi S3] → Run workflow**:

1. Select `main` and operation `preview`.
2. Review the proposed resources and account/stack/backend.
3. Start another run with operation `deploy`.

The action builds all Lambda ZIP archives before running Pulumi; you do not build them locally for GitHub deployment. Preview does not create resources and cannot prove that every AWS quota or service prerequisite will allow creation.

## 6. Verify SES DNS and resume if necessary

Pulumi creates the SES identity and DKIM tokens. Cognito uses that identity for email and requires it to be verified. Currently the infrastructure does **not wait for SES verification**, so a first deployment can create resources and then fail at Cognito.

In the deployment's AWS account, open **SES in us-east-2**, select `info.attestrabond.com` (or your configured `senderDomain`), and follow [Cloudflare DNS instructions](../README.md#configure-sender-dns-in-cloudflare). They include read-only AWS CLI commands for retrieving records if stack outputs are unavailable after a partial deployment.

For the example sender `verify@info.attestrabond.com`:

- Add or update the `_amazonses.info` TXT value and the three DKIM CNAME names/targets using the current SES values.
- Keep CNAMEs **DNS only**. Preserve unrelated website/R2 and mailbox records.
- Compare existing records before changing them: recreating an identity does not necessarily change every token.
- The identity `info.attestrabond.com` must be verified in the correct account and region. An unverified separate root identity `attestrabond.com` does not block sending with this verified subdomain identity.

Wait for verification/DKIM success, then run the deployment again. Keep the existing stack and S3 state; Pulumi resumes the partial deployment. SES sandbox recipient restrictions are a separate concern: see [SES sending for test recipients](../README.md#enable-ses-sending-for-test-recipients).

## 7. Connect Android

After successful deployment, use the new `apiUrl`, `userPoolId` and `clientId` outputs to [configure Android](../README.md#configure-the-android-api-url). A fresh deployment can change all three. Infrastructure deployment does not automatically enable real ID capture: `captureEnabled` remains false until its document type, purpose and jurisdiction are configured deliberately.

## Routine deployments

Once bootstrap is complete, push reviewed changes to `main` and run **Deploy AWS [Pulumi S3]**, optionally previewing separately first. You do not rerun setup, initialize the stack or change DNS for each code update.

Rerun setup when changing GitHub environment settings or its managed AWS role configuration. After a code/configuration fix, start a **new workflow run on main**; retrying an old run reuses the old commit.

## Common blockers

| Symptom | Next step |
| --- | --- |
| Missing `dist/capture.zip` during local preview | Run the repository build script first, or use the deploy tool/action that builds automatically. |
| `aws login` says profile has SSO credentials | Use SSO login / the wizard's SSO option for that profile. |
| Missing bucket or stack | Complete steps 2–3; the action does not bootstrap them. |
| `aws:profile` rejected | Remove it from the stack YAML, commit and push; keep local profile selection in your shell. |
| Passphrase must be set while saving `proofKey` | Set `PULUMI_CONFIG_PASSPHRASE` through hidden input before piping the key, as in step 3. |
| Secret decryption error / `bad value` | Check matching YAML encryption metadata, backend secrets provider and GitHub passphrase. Old Cloud ciphertext must be migrated, not copied into a fresh passphrase stack. |
| Cognito says SES identity is unverified | Check the exact identity ARN/account/region from the error; complete step 6. |
| Reserved concurrency would reduce unreserved capacity below its minimum | Use dev's shared-pool configuration or request adequate regional quota for reservations. See [per-stack concurrency](github-deployment.md#capture-concurrency-per-environment). |

Dev uses `captureReservedConcurrency: -1` and `captureWorkerMaxConcurrency: 2`. Other stacks default to 5 reserved executions **per capture function** (15 total) and a worker maximum of 5 unless overridden. The worker queue limit does not reserve capacity; all dev functions still share the account's available concurrency.
