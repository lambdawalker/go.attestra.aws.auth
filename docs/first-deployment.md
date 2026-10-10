# First deployment from scratch

Run the Go terminal wizard to create or resume a deployment for `dev`, `qa`, `prod`, or a custom environment. It handles AWS login, S3 state, Pulumi stack/configuration, IAM/OIDC and GitHub environment setup, then guides deployment, SES DNS verification and Android configuration. The interface uses Charm Huh/Lipgloss. Launchers invoke Go directly; no PowerShell wrapper is required.

## Before you start

You still need an AWS account, a GitHub repository checkout, an authorized AWS identity, and access to your domain's DNS. The wizard cannot create an AWS account, grant permissions to the identity running it, or register a domain.

Install **Git**, **Go 1.26.6+**, **AWS CLI v2** and **Pulumi CLI** on PATH. The workflow pins Pulumi 3.264.0. Choose a current AWS CLI supporting `aws configure export-credentials`; browser login requires v2.32.0+. No Python, GitHub CLI or external ZIP utility is needed for local setup/build/deployment. CI separately uses Python for archive verification.

Create a fine-grained GitHub token restricted to this repository with **Administration: read/write**, **Environments: read/write**, **Actions: read**, and **Metadata: read**. The wizard prompts for it with hidden input. It does not save the token. An AWS administrator must authorize S3 bucket creation/configuration, state access and the [IAM bootstrap operations](github-deployment.md#guided-environment-setup). Optional local deployment also requires permissions to provision application resources.

If you already have application resources managed in Pulumi Cloud, [migrate their state](deployment.md#one-time-migration-from-pulumi-cloud) first. Do not initialize an empty S3 stack for those existing resources. A `.bak` YAML file alone is not resource state.

## Run the wizard

Windows, from the repository root (PowerShell or Command Prompt):

```text
.\setup.bat
```

Linux/macOS:

```sh
./setup.sh
```

`setup-github.bat` and `setup-github.sh` are aliases for the same full wizard. You can also invoke Go directly:

```sh
go -C tools/deploy run . -bootstrap
```

For **IAM/GitHub maintenance only**, with an existing bucket/stack:

```sh
go -C tools/deploy run . -setup-github
```

## What the wizard does

### 1. Choose the repository and AWS identity

Enter the GitHub token and repository. Select **dev**, **qa**, **prod**, an existing environment, or **Create another environment** and enter your own name. Names use 1–32 lowercase letters/digits with single internal hyphens and start with a letter (for example `demo-2`). The GitHub environment and Pulumi stack use the same name. The wizard reads only that environment’s settings and asks for its deployment region and S3 backend URL.

For a fresh deployment it proposes a random `attestra-state-...` bucket name, avoiding the existing project's bucket name. S3 names must be globally unique. Existing environment values take precedence. Non-secret selections (repository/region/backend/stack) are saved to ignored `bootstrap.<environment>.local.json`, so an interrupted first setup can reuse its bucket before the GitHub environment exists. Keep this file locally when resuming. Legacy `bootstrap.local.json` is read only for `dev`. A checkpoint from another environment is never reused.

Choose SSO, AWS browser login, or access-key credentials. For SSO, the wizard can open `aws configure sso` to configure a new profile, then runs `aws sso login`. Use an existing SSO profile with the SSO option, not `aws login`. Temporary credentials require an access key, secret key **and** session token. Credentials are checked with STS; an account mismatch with an existing GitHub environment stops setup.

### 2. Prepare the state bucket and stack

The wizard shows proposed changes and asks before applying them. It:

- Creates a missing S3 bucket only after an explicit not-found response. Access denied is never treated as a missing bucket.
- Verifies existing bucket ownership and region, then ensures versioning and all four public access blocks are enabled. Existing objects and encryption settings are preserved; new S3 buckets use default encryption.
- Prompts for and confirms the Pulumi passphrase. For an existing stack, use its **original** passphrase. Save it in a password manager.
- Lists stacks in the selected project/backend before initializing a missing stack. Failed reads stop setup. No stack removal, resource destruction or automatic state migration occurs.
- For a fresh stack, offers to retain an existing local `Pulumi.<environment>.yaml` as an ignored `.bak.<timestamp>` file before creating fresh encryption metadata.
- Fills missing region, app origin, sender domain/address and environment-specific concurrency settings. Existing values are retained; use Pulumi config explicitly to change existing settings.
- Generates a cryptographically random 32-byte proof key **only if missing**, passing it through stdin to `pulumi config set --secret`. Existing encrypted keys are retained. Decrypted configuration is captured only in memory; secret command output is suppressed.

The passphrase is supplied to Pulumi subprocesses through their environment, including when piping the proof key, so Pulumi does not need to prompt on stdin. A wrong passphrase or incompatible existing YAML/provider configuration stops configuration writes. An interrupted initialization is resumed with the same passphrase rather than starting again.

The state bucket is separate from the application/ID-evidence bucket. Keep state versioning and do not add evidence expiry rules to it. S3 state avoids the Pulumi Cloud API, but AWS storage/requests can incur charges.

### Domain defaults for each environment

Enter a base domain such as `example.com`. Setup offers editable defaults:

| Environment | App origin | SES identity | Sender |
| --- | --- | --- | --- |
| `dev` | `https://dev.example.com` | `dev.info.example.com` | `verify@dev.info.example.com` |
| `qa` | `https://qa.example.com` | `qa.info.example.com` | `verify@qa.info.example.com` |
| `prod` | `https://prod.example.com` | `prod.info.example.com` | `verify@prod.info.example.com` |
| `demo-2` | `https://demo-2.example.com` | `demo-2.info.example.com` | `verify@demo-2.info.example.com` |

All environments, including production, receive the prefix. App origin drives email links, CORS, and WebAuthn origin/RP ID settings. The API Gateway URL is already generated separately for each stack; this repository has no custom API domain setting. Host your verification page and Android association files on each app origin and configure its DNS/HTTPS separately; this backend wizard does not provision that website.

Existing stack configuration wins over proposed defaults. In particular, the deployed dev stack keeps `https://attestrabond.com` and `info.attestrabond.com`. Moving it to `dev.*` is an explicit configuration change requiring DNS/SES verification and frontend/Android updates; setup does not silently rename live identities.

### 3. Configure IAM and GitHub

The wizard inspects or creates the GitHub OIDC provider and setup-managed deployment role, displays the trust/permission policies for review, applies approved changes and verifies them. It obtains the actual `AWS_ROLE_ARN` from AWS. The default role is `attestra-github-deploy-<environment>`, with an exact repository/environment OIDC subject. Existing configured role names (including the original dev role) are retained. A role owned by another environment is rejected. Unmanaged existing roles are left unchanged; choose a new dedicated role name.

It creates or updates the selected GitHub environment, preserving existing protection rules and restricting a new environment to `main`. It saves account, region, role ARN, backend and stack variables. The **same tested stack passphrase** is uploaded as `PULUMI_CONFIG_PASSPHRASE` using GitHub's encrypted secret upload. No AWS access keys or Pulumi Cloud token are stored in GitHub. The environment appears automatically in the Deploy action dropdown once created; no workflow edits are needed. Commit the stack YAML before running it.

For Cloudflare DNS, leave the optional Route 53 hosted zone blank. That field grants IAM access for a separately configured Route 53 integration; it does not configure Cloudflare.

### 4. Review configuration and deploy

The wizard prints the commands to review, commit and push `infra/Pulumi.<environment>.yaml`. It does **not** run Git commit/push for you. Commit the encrypted `proofKey` and encryption salt, not plaintext secrets, state exports or backup files. Do not set `aws:profile` in the committed YAML; GitHub uses temporary OIDC credentials.

Then choose:

- **Finish here; deploy using GitHub Actions**: follow the printed workflow URL. Select the new environment in the **environment dropdown**, then start a new `main` run with operation `preview`, review it, then run `deploy`.
- **Build and preview locally**: uses the selected backend/account and does not deploy resources.
- **Build, preview and deploy locally**: invokes the Go packager, previews and runs `pulumi up` with Pulumi's confirmation prompt. It deploys your current checkout, so review local changes first.
- **Configure Cloudflare DNS for SES**: prompts for a zone-scoped token, shows the exact proposed changes, creates missing SES records, and verifies them.
- **Show SES DNS records and check verification**: reads the created SES identity and displays the exact TXT/CNAME records and verification status.
- **Show deployed Android configuration**: prints `apiUrl`, `userPoolId` and `clientId` after a successful deployment.

The deployment menu stays open after a local failure so you can inspect SES or retry after addressing the reported error. Credentials are a snapshot of the AWS session; if they expire, rerun setup and authenticate again. Neither the wizard nor the action rolls back successful resources on failure.

## SES and Cloudflare DNS setup

Pulumi creates the SES identity/DKIM tokens, but it does **not wait for verification** before creating Cognito. A first deployment can therefore fail at Cognito while SES is still pending. Preview alone cannot expose every AWS service prerequisite or quota limitation.

After the first deployment attempt, choose **Configure Cloudflare DNS for SES**. Create an API token in [Cloudflare API Tokens](https://dash.cloudflare.com/profile/api-tokens) with **Zone → Zone → Read** and **Zone → DNS → Edit**, restricted to your authoritative zone (for example `attestrabond.com`). Enter it at the hidden prompt and select the matching active zone. Review the TXT and three DKIM CNAME records, then confirm the changes.

The token stays in memory and is never saved to GitHub, Pulumi configuration, or local checkpoints. Matching records are reused; missing records are created with TTL 300. A matching proxied CNAME can be changed to **DNS only** after review. Different CNAME targets or incompatible record types stop setup for manual review, before applying the plan. Additional TXT values and unrelated R2/website/mailbox records are preserved. The wizard does not delete records or retry failed writes automatically. Rerun after a partial failure to reuse completed records.

The wizard refuses Cloudflare setup when `route53ZoneId` is configured, to avoid two DNS managers for the same records. Cloudflare records are managed by this optional setup step, not by Pulumi; destroying a stack does not remove them. Keep DKIM CNAME flattening disabled in Cloudflare, including the zone-wide “flatten all CNAMEs” option. The wizard does not change zone-wide settings or DNS delegation.

Alternatively, use **Show SES DNS records and check verification** and copy the displayed records manually. [Detailed DNS instructions and manual verification commands](../README.md#configure-sender-dns-in-cloudflare) are available if needed.

Wait for verification and DKIM success; choose the check again to reread status. Then resume deployment using the existing stack. Do not tear down resources or delete state. A verified `info.attestrabond.com` covers `verify@info.attestrabond.com`; an unverified separate root identity `attestrabond.com` is not a blocker. Verify in the correct AWS account and region (`us-east-2` for the current deployment). SES sandbox restrictions on recipients are [a separate step](../README.md#enable-ses-sending-for-test-recipients).

## Connect Android

Use the deployed `apiUrl`, `userPoolId` and `clientId` outputs to [configure Android](../README.md#configure-the-android-api-url). These can change after recreating infrastructure. The backend does not deploy your website's verification page or Android association files.

Real ID capture remains disabled until `captureEnabled`, document type, purpose and jurisdiction are deliberately configured. Dev uses `captureReservedConcurrency: -1` (shared capacity) and `captureWorkerMaxConcurrency: 2`; see [per-stack concurrency](github-deployment.md#capture-concurrency-per-environment) for QA/prod settings and quotas. New non-dev stacks default to reservation `5` per capture function and worker maximum `5`; confirm regional quota before deployment.

## Routine updates and recovery

Once bootstrap is complete, push reviewed changes to `main` and run **Deploy AWS [Pulumi S3]**, selecting the matching environment. Setup and DNS changes are not required for every code deployment. After a code/config fix, start a **new** workflow run; retrying an old run reuses the old commit.

Rerun the wizard to resume setup, retaining `bootstrap.<environment>.local.json` and the stack YAML. Existing resources/configuration are inspected and reused. Review confirmations: existing bucket protections are reasserted and the tested passphrase is saved to GitHub again. Cancelling after earlier stages leaves those changes intact.

| Symptom | Next step |
| --- | --- |
| Missing tools | Install the named tool and restart the terminal; rerun the wizard. |
| SSO profile rejected by browser login | Choose SSO and, if needed, configure the profile through the wizard. |
| S3 access denied | Check ownership and permissions. The wizard will not assume the bucket is missing. |
| Existing YAML or passphrase cannot decrypt secrets | Use matching YAML and the original passphrase; migrate Cloud state instead of copying ciphertext into a new stack. |
| Profile override rejected | Remove `aws:profile` from stack config; use local profile selection only for AWS login. |
| Cognito says SES identity is unverified | Check the exact identity/account/region and complete DNS verification, then resume. |
| Lambda reserved concurrency exceeds available quota | Use dev's shared-pool values or request sufficient regional quota for reservations. Existing config is retained by setup, so update it deliberately if needed. |
| Local preview cannot find Lambda ZIPs | Use `build.bat` / `build.sh`, or the wizard/deploy tool which builds automatically. |
| Partial application deployment | Keep state and fix the reported prerequisite; rerun deployment. |

The wizard does not request Lambda quota increases, leave the SES sandbox, provision website hosting or rotate existing proof keys automatically. Cloudflare DNS changes require the dedicated menu option and confirmation; manual DNS setup remains available.

## Remove an environment

Use `teardown.bat` or `./teardown.sh` for a reviewed, resumable teardown of the selected stack, GitHub environment, deployment role, and SES Cloudflare records. See [teardown and recovery](teardown.md). The state bucket and shared OIDC provider are retained.
