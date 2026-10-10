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
- **Build, preview and deploy**: builds archives, deploys SES prerequisites, configures DNS, waits for verification, then previews and deploys the full application without additional deployment prompts. It deploys your current checkout, so review local changes first.
- **Configure Cloudflare DNS for SES**: prompts for a zone-scoped token, shows the exact proposed changes, creates missing SES records, and verifies them.
- **Show SES DNS records and check verification**: reads the created SES identity and displays the exact TXT/CNAME records and verification status.
- **Show deployed Android configuration**: prints `apiUrl`, `userPoolId` and `clientId` after a successful deployment.

The deployment menu stays open after a local failure so you can inspect SES or retry after addressing the reported error. Credentials are a snapshot of the AWS session; if they expire, rerun setup and authenticate again. Neither the wizard nor the action rolls back successful resources on failure.

## SES and Cloudflare DNS setup

The staged wizard creates SES identity/DKIM tokens first and waits for verification before deploying Cognito. Direct `pulumi up` bypasses the wizard and can still fail if SES is unverified. Preview alone cannot expose every AWS service prerequisite or quota limitation.

Choose **Build, preview and deploy** and follow its DNS stage. You can also use **Configure Cloudflare DNS for SES** separately once the SES prerequisites exist. Create an API token in [Cloudflare API Tokens](https://dash.cloudflare.com/profile/api-tokens) with **Zone → Zone → Read** and **Zone → DNS → Edit**, restricted to your authoritative zone (for example `attestrabond.com`). Enter it at the hidden prompt and select the matching active zone. Review the TXT and three DKIM CNAME records, the wizard applies the displayed changes automatically.

The token stays in memory and is never saved to GitHub, Pulumi configuration, or local checkpoints. Matching records are reused; missing records are created with TTL 300. A matching proxied CNAME can be changed to **DNS only** after displaying the planned changes. Different CNAME targets or incompatible record types stop setup for manual review, before applying the plan. Additional TXT values and unrelated R2/website/mailbox records are preserved. The wizard does not delete records or retry failed writes automatically. Rerun after a partial failure to reuse completed records.

The wizard refuses Cloudflare setup when `route53ZoneId` is configured, to avoid two DNS managers for the same records. Cloudflare records are managed by this optional setup step, not by Pulumi; destroying a stack does not remove them. Keep DKIM CNAME flattening disabled in Cloudflare, including the zone-wide “flatten all CNAMEs” option. The wizard does not change zone-wide settings or DNS delegation.

Alternatively, use **Show SES DNS records and check verification** and copy the displayed records manually. [Detailed DNS instructions and manual verification commands](../README.md#configure-sender-dns-in-cloudflare) are available if needed.

Wait for verification and DKIM success; choose the check again to reread status. Then resume deployment using the existing stack. Do not tear down resources or delete state. A verified `info.attestrabond.com` covers `verify@info.attestrabond.com`; an unverified separate root identity `attestrabond.com` is not a blocker. Verify in the correct AWS account and region (`us-east-2` for the current deployment). SES sandbox restrictions on recipients are [a separate step](../README.md#enable-ses-sending-for-test-recipients).

## Connect Android

Use the deployed `apiUrl`, `userPoolId` and `clientId` outputs to [configure Android](../README.md#configure-the-android-api-url). These can change after recreating infrastructure. The backend does not deploy your website's verification page or Android association files.

Real ID capture remains disabled until `captureEnabled`, document type, purpose and jurisdiction are deliberately configured. Dev uses `captureReservedConcurrency: -1` (shared capacity) and `captureWorkerMaxConcurrency: 2`; see [per-stack concurrency](github-deployment.md#capture-concurrency-per-environment) for QA/prod settings and quotas. New non-dev stacks default to reservation `5` per capture function and worker maximum `5`; confirm regional quota before deployment.

## Routine updates and recovery

Once bootstrap is complete, push reviewed changes to `main` and run **Deploy AWS [Pulumi S3]**, selecting the matching environment. Setup and DNS changes are not required for every code deployment. After a code/config fix, start a **new** workflow run; retrying an old run reuses the old commit.

Rerun the wizard to resume setup, retaining `bootstrap.<environment>.local.json` and the stack YAML. Existing resources/configuration are inspected and reused. State buckets are created automatically when absent, or reused after ownership and region checks; versioning and public-access blocks are ensured without a separate confirmation. The tested passphrase is saved to GitHub again after its configuration confirmation. Cancelling after earlier stages leaves those changes intact.

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

The wizard does not request Lambda quota increases, leave the SES sandbox, provision website hosting or rotate existing proof keys automatically. Cloudflare DNS changes are applied through the DNS stage or dedicated menu option; manual DNS setup remains available.

## Remove an environment

Use `teardown.bat` or `./teardown.sh` for a reviewed, resumable teardown of the selected stack, GitHub environment, deployment role, and SES Cloudflare records. See [teardown and recovery](teardown.md). The state bucket and shared OIDC provider are retained.

## Ordered first deployment and DNS verification

After configuration, the setup wizard automatically performs these stages in order. The recovery menu also offers **Build, preview and deploy**:

1. Build Lambda archives, then preview and apply a targeted Pulumi update for the SES identity and DKIM configuration only. If `route53ZoneId` is configured, its SES verification records are included. Other application resources are retained, not removed or deployed by this stage.
2. If SES is not already verified, configure Cloudflare DNS through the wizard, or display the records for manual publication. You can also pause and resume later. Route53-managed records do not prompt for a Cloudflare token.
3. Poll AWS every 15 seconds until both identity verification and DKIM report `Success`. Pending verification blocks the full deployment. Ctrl+C interrupts this wait and returns to the menu; resources are retained. Failed verification or an AWS read error stops the sequence with an explanation.
4. Preview and apply the full application deployment. Both Pulumi updates run automatically after preview, without another deployment confirmation.

Rerun the same option to resume. The wizard reads actual Pulumi/AWS state; an already verified identity skips DNS configuration and waiting. DNS propagation can take time, so pausing does not require teardown or a new stack. Builds still run before targeted updates because Pulumi evaluates the entire program, including local Lambda archive references.

GitHub Actions deploy checks SES readiness first and gives instructions to complete setup if the identity is missing or unverified. Preview-only remains available. Complete this initial staged setup locally before deploying subsequent changes through Actions. The state backend and stack are shared by both paths.

## Automatic setup and resume memory

Full setup continues automatically from configuration through IAM/GitHub setup, SES prerequisites, DNS publication, verification polling and complete application deployment. It displays plans before applying them without repeated deployment approvals. On success it prints the API/Cognito values needed by Android and exits. If a phase fails, successful work is retained and the recovery menu remains available. The standalone GitHub-only setup keeps its existing confirmations. Stack migration and replacing local encryption metadata still require explicit decisions.

The ignored `bootstrap.<environment>.local.json` now remembers non-secret configuration choices, AWS authentication mode/profile, account and deployment role, application domains, DNS choices, and the latest completed deployment phase. Writes use a temporary file and a recovery backup; `.tmp` and `.bak` files are also ignored. A backup is read if replacement was interrupted. Keep these files private and run only one setup instance for an environment at a time.

On rerun, choose the repository/environment and supply credentials. Existing GitHub values take precedence over checkpoint defaults; current Pulumi application configuration takes precedence over remembered answers. Previously answered configuration prompts are skipped. No tokens, AWS keys, passphrases or proof keys are stored in this memory file. GitHub tokens, the stack passphrase and any required Cloudflare token are still requested; AWS SSO/login may need to authenticate again.

Progress is a resume aid, not proof that resources still exist: AWS/Pulumi are checked again. Completed DNS publication is remembered using a fingerprint of the SES record names and values; changed tokens require DNS setup again. Pending verification resumes polling without asking for the same Cloudflare token. To intentionally change a remembered non-secret choice, edit its `Settings` entry in the ignored checkpoint while setup is stopped; change established application configuration with Pulumi config. Never remove or alter deployment state just to reset wizard prompts.

A deployed backend still requires SES sandbox recipient verification or production access for general email delivery, plus configuring the Android app and any separately hosted website. The wizard does not claim those external steps are complete.

## API custom domains

Setup now fills missing `attestra-auth-email:apiDomain` using your selected base domain:

| Environment | Default API hostname |
| --- | --- |
| dev | `dev.api.attestrabond.com` |
| qa | `qa.api.attestrabond.com` |
| prod | `api.attestrabond.com` |
| custom | `<environment>.api.attestrabond.com` |

Existing `apiDomain` configuration is retained. When upgrading an existing environment, rerun setup to add this setting and refresh its deployment-role permissions before using Actions.

The prerequisite update requests an ACM certificate in the API's region alongside the SES resources. Before deploying the complete application, the wizard publishes the ACM validation CNAME through Cloudflare and polls until ACM reports `ISSUED`. It then creates the regional API Gateway custom domain and root API mapping, and publishes a DNS-only CNAME pointing to the **custom-domain target**, not the raw API URL. An existing Cloudflare token from SES setup is reused only in memory. Matching records are reused; conflicting records stop setup without replacement. Certificate validation records must remain for renewal. Keep CNAME flattening disabled for validation records.

If `route53ZoneId` is configured, Pulumi manages both the certificate-validation record and API CNAME in that zone. Cloudflare is not used for those records.

The `apiUrl` output becomes `https://<apiDomain>`, retaining existing API route paths. `rawApiUrl` retains the AWS-generated endpoint for diagnostics and compatibility. The raw endpoint remains enabled; both use the same API authentication. `apiCertificateArn`, `apiDomain`, and `apiDomainTarget` are also exported. Without an `apiDomain` setting, direct Pulumi deployment retains the original raw URL behavior.

GitHub deployment checks the configured certificate is issued before proceeding. First-time certificate validation and Cloudflare publication run through local setup; if DNS publication fails after AWS deployment, rerun setup to finish it. DNS propagation can delay client access after successful publication. This does not create website hosting or change Cognito IDs, and Android still needs its base URL configured from `apiUrl`.

## Lambda build performance

The Go packager builds independent Lambda entry points concurrently with up to four workers (reduced on smaller machines). Go package parallelism is divided among workers. All six `auth-*` archives reuse one compiled `cmd/signin` binary: 15 Lambda archives require only 10 unique builds. A compilation failure cancels running compiler processes, stops remaining work, and prevents deployment.

Within one staged setup run, the full deployment reuses the archives built for the prerequisite phase and checks that all archives are still present. A new setup attempt builds again through Go's build cache, so changed sources are picked up. If you edit code while the wizard is waiting for DNS, restart setup to include those edits; the current run uses its earlier build. Both partial and full deployments wait until compilation and packaging have finished.
