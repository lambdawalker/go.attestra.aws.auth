# First deployment from scratch

Run the Go terminal wizard to create or resume a deployment for `dev`, `qa`, `prod`, or a custom environment. It handles AWS login, S3 state, Pulumi stack/configuration, IAM/OIDC and GitHub environment setup, then guides deployment, SES DNS verification and Android configuration. The interface uses Charm Huh/Lipgloss. Launchers invoke Go directly; no PowerShell wrapper is required.

## Before you start

You still need an AWS account, a GitHub repository checkout, an authorized AWS identity, and access to your domain's DNS. The wizard cannot create an AWS account, grant permissions to the identity running it, or register a domain.

Install **Git**, **Go 1.26.6+**, **AWS CLI v2** and **Pulumi CLI** on PATH. The workflow pins Pulumi 3.264.0. Choose a current AWS CLI supporting `aws configure export-credentials`; browser login requires v2.32.0+. No Python, GitHub CLI or external ZIP utility is needed for local setup/build/deployment. CI separately uses Python for archive verification.

Create a fine-grained GitHub token restricted to this repository with **Administration: read/write**, **Environments: read/write**, **Actions: read**, and **Metadata: read**. The wizard prompts for it with hidden input. It only saves credentials if you opt into the encrypted local vault. An AWS administrator must authorize S3 bucket creation/configuration, state access and the [IAM bootstrap operations](github-deployment.md#guided-environment-setup). Optional local deployment also requires permissions to provision application resources.

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

## Optional encrypted local credentials

After DNS selection, stack preparation, and collection of the required credentials, full setup asks **Save these credentials in an encrypted local vault?** The default is **No**. If accepted, it saves the GitHub token, Cloudflare token when applicable, Pulumi stack passphrase, and manually entered AWS access key/secret/session token. AWS SSO/browser-login credentials remain managed by the AWS CLI and are not copied into this vault. The application proof key remains in Pulumi's encrypted configuration.

Each repository/environment has its own vault under the OS user configuration directory:

- Windows: `%AppData%\attestra\credentials\<scope-hash>.vault.enc`
- Linux: `$XDG_CONFIG_HOME/attestra/credentials/`, or `~/.config/attestra/credentials/`
- macOS: `~/Library/Application Support/attestra/credentials/`

The exact path is printed after saving. Vault files are outside the checkout by default, with an additional Git ignore rule for the encrypted files and temporary files. Unix directories/files use `0700`/`0600`; Windows uses the user profile's inherited ACLs. Do not share the vault: it contains reusable credentials protected by your passphrase.

Encryption uses Go's AES-256-GCM with a fresh random nonce for every save, and Argon2id (64 MiB, 3 iterations, 1 lane) with a random 16-byte salt to derive the key. The versioned format fixes KDF parameters and limits file size. Repository/environment identity is authenticated with the ciphertext, so a vault copied to another scope cannot be unlocked there. Only encrypted bytes reach disk, including temporary files. Failed unlocks do not overwrite existing files. The encryption key is cleared on wizard cleanup; Go does not guarantee erasure of every in-memory string copy.

The new vault passphrase is entered twice and must differ from the Pulumi passphrase. Validation requires 20–1024 Unicode characters, allows spaces/Unicode, normalizes Unicode to NFC, and rejects common examples and repetitive patterns. There are no uppercase/digit/symbol requirements. Choose at least five randomly selected words or a long password-manager-generated secret; validation cannot guarantee entropy or check every breached password. The [OWASP encryption guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html), [Argon2id guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html), and [NIST password guidance](https://pages.nist.gov/800-63-4/sp800-63b/passwords/) inform this policy; this is not a claim of NIST/FIPS certification.

On rerun, when setup is needed, choose to unlock the saved vault after repository selection. Its credentials fill the necessary steps. An unlocked vault is updated with credentials used in that run. Choosing not to unlock uses fresh input, and replacing the existing vault requires an explicit confirmation. The unchanged-completed-setup shortcut still exits before requesting a vault passphrase.

For expired, revoked, or incorrect credentials, bypass both the completion shortcut and saved credentials:

```text
.\setup.bat -fresh-credentials
```

```sh
./setup.sh -fresh-credentials
```

This offers replacement after new credentials are collected. Saving temporary AWS credentials does not extend their validity. There is no vault-passphrase recovery: re-enter the underlying credentials and replace the vault with a new passphrase. Keep both your vault and Pulumi passphrases recoverable in a password manager. Removing the vault file removes the local saved copy; it does not revoke the tokens at their providers. Standalone deploy, teardown, and IAM-only maintenance do not unlock the setup vault.

## Fast reruns without remote calls

After you select an environment, setup checks its local completion record before asking for tokens, checking AWS/Pulumi prerequisites, or contacting AWS, GitHub, or Cloudflare. If the last full wizard deployment succeeded at the current Git commit, the working tree is clean, the repository/environment match, and the Pulumi YAML and saved settings are unchanged, it reports **all set according to the local setup record** and exits.

This is a local shortcut, not a health or drift check. It cannot detect manual cloud changes, expired credentials, a deployment or teardown from another machine, or GitHub Actions changes. GitHub Actions does not update this local record. To bypass it and run the normal setup/reconciliation:

```text
.\setup.bat -force-setup
```

```sh
./setup.sh -force-setup
```

A different commit resumes the normal setup/deploy flow, including for documentation-only commits. Staged, unstaged, and untracked changes trigger: **Your working tree has uncommitted changes (staged, unstaged, or untracked). Setup will build and deploy these local changes. Continue?** Cancel stops before requesting credentials or contacting services. Ignored build artifacts and local checkpoints do not trigger this prompt.

A deployment from a dirty checkout, or one whose commit/configuration changes during deployment, does not get a clean-commit completion receipt. Commit the changes and rerun setup to establish it. This also applies when first setup creates or changes `infra/Pulumi.<environment>.yaml`. Older checkpoints without a receipt require one normal successful run. Missing local records require normal setup as well. Keep checkpoints locally; no credentials or passphrases are stored in the receipt.

Local teardown checkpoint files also disable the shortcut. Follow the teardown recovery instructions and archive a completed teardown checkpoint before reusing that environment. The `-setup-github` maintenance mode always contacts services and does not use the completed-setup shortcut.

## What the wizard does

### 1. Choose the environment, then the repository and AWS identity

The first question selects **dev**, **qa**, **prod**, an environment found in local checkpoints/Pulumi YAML files, or **Use another environment** to enter an existing or new name. This menu makes no remote calls; an environment that only exists on GitHub can be entered by name. If setup is needed, enter the GitHub token and repository next. Names use 1–32 lowercase letters/digits with single internal hyphens and start with a letter (for example `demo-2`). The GitHub environment and Pulumi stack use the same name. The wizard reads only that environment’s settings and asks for its deployment region and S3 backend URL.

For a fresh deployment it proposes a random `attestra-state-...` bucket name, avoiding the existing project's bucket name. S3 names must be globally unique. Existing environment values take precedence. Non-secret selections (repository/region/backend/stack) are saved to ignored `bootstrap.<environment>.local.json`, so an interrupted first setup can reuse its bucket before the GitHub environment exists. Keep this file locally when resuming. Legacy `bootstrap.local.json` is read only for `dev`. A checkpoint from another environment is never reused.

Choose SSO, AWS browser login, or access-key credentials. For SSO, the wizard can open `aws configure sso` to configure a new profile, then runs `aws sso login`. Use an existing SSO profile with the SSO option, not `aws login`. Temporary credentials require an access key, secret key **and** session token. Credentials are checked with STS; an account mismatch with an existing GitHub environment stops setup.

### 2. Prepare the state bucket and stack

The wizard shows proposed changes and asks before applying them. It:

- Creates a missing S3 bucket only after an explicit not-found response. Access denied is never treated as a missing bucket.
- Verifies existing bucket ownership and region, then ensures versioning and all four public access blocks are enabled. Existing objects and encryption settings are preserved; new S3 buckets use default encryption.
- Prompts for and confirms the Pulumi passphrase. For an existing stack, use its **original** passphrase. Save it in a password manager.
- Lists stacks in the selected project/backend and automatically reuses an existing stack or initializes a new one, without a confirmation prompt. Failed or invalid reads stop setup. No stack removal, resource destruction or automatic state migration occurs.
- If the stack is missing but its local YAML, YAML backups, or checkpoint indicate previous setup, stops with migration/restore guidance instead of overwriting configuration or starting with empty state. Select the original backend or migrate/restore the state. After a deliberate teardown, archive the old environment YAML, backups, and bootstrap checkpoint before starting fresh. A GitHub backend that conflicts with remembered initialized state also stops setup.
- Fills missing region, app origin, sender domain/address and environment-specific concurrency settings. Existing values are retained; use Pulumi config explicitly to change existing settings.
- Generates a cryptographically random 32-byte proof key **only if missing**, passing it through stdin to `pulumi config set --secret`. Existing encrypted keys are retained. Decrypted configuration is captured only in memory; secret command output is suppressed.

The passphrase is supplied to Pulumi subprocesses through their environment, including when piping the proof key, so Pulumi does not need to prompt on stdin. A wrong passphrase or incompatible existing YAML/provider configuration stops configuration writes. An interrupted initialization is resumed with the same passphrase rather than starting again.

The state bucket is separate from the application/ID-evidence bucket. Keep state versioning and do not add evidence expiry rules to it. S3 state avoids the Pulumi Cloud API, but AWS storage/requests can incur charges.

### Choose the DNS provider once

Setup asks **Which service manages DNS for your domain?** with **Amazon Route 53** and **Cloudflare** options. Choose the service hosting the authoritative DNS records; this does not transfer your domain or change nameservers.

- **Amazon Route 53:** enter the existing public hosted zone ID. Setup saves `attestra-auth-email:route53ZoneId`, grants the deployment role access to that zone, and uses Pulumi to publish SES verification/DKIM, ACM certificate validation, and the API hostname records. No Cloudflare prompts appear.
- **Cloudflare:** setup leaves Route 53 management disabled. When the AWS verification records exist, it asks for a zone-scoped API token and publishes the records, reusing that token in memory for certificate validation and the API hostname. It does not ask you to choose a DNS provider again during deployment. If there is only one matching Cloudflare zone, it is selected automatically.

The choice is remembered per environment. Existing Pulumi Route 53 configuration takes precedence; older remembered hosted zone IDs are reused, and an older blank Route 53 answer maps to Cloudflare. Changing providers for an established environment requires a deliberate DNS migration; editing a remembered choice does not move DNS delegation or remove existing records.

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

The deployment menu stays open after a local failure so you can inspect SES or retry after addressing the reported error. SSO and browser-login credentials renew automatically while the underlying session allows; if that session ends, sign in again and rerun setup to resume. Manually pasted credentials cannot renew automatically. GitHub Actions continues to use OIDC. Neither the wizard nor the action rolls back successful resources on failure.

## SES and Cloudflare DNS setup

The staged wizard creates SES identity/DKIM tokens first and waits for verification before deploying Cognito. Direct `pulumi up` bypasses the wizard and can still fail if SES is unverified. Preview alone cannot expose every AWS service prerequisite or quota limitation.

Choose **Build, preview and deploy** and follow its DNS stage. You can also use **Configure Cloudflare DNS for SES** separately once the SES prerequisites exist. Create an API token in [Cloudflare API Tokens](https://dash.cloudflare.com/profile/api-tokens) with **Zone → Zone → Read** and **Zone → DNS → Edit**, restricted to your authoritative zone (for example `attestrabond.com`). Enter it at the hidden prompt and select the matching active zone. Review the TXT and three DKIM CNAME records, the wizard applies the displayed changes automatically.

The token stays in memory unless you opt into the encrypted local vault; it is never saved to GitHub, Pulumi configuration, or plaintext local checkpoints. Matching records are reused; missing records are created with TTL 300. A matching proxied CNAME can be changed to **DNS only** after displaying the planned changes. Different CNAME targets or incompatible record types stop setup for manual review, before applying the plan. Additional TXT values and unrelated R2/website/mailbox records are preserved. The wizard does not delete records or retry failed writes automatically. Rerun after a partial failure to reuse completed records.

The wizard refuses Cloudflare setup when `route53ZoneId` is configured, to avoid two DNS managers for the same records. Cloudflare records are managed by the selected provider’s setup step, not by Pulumi; destroying a stack does not remove them. Keep DKIM CNAME flattening disabled in Cloudflare, including the zone-wide “flatten all CNAMEs” option. The wizard does not change zone-wide settings or DNS delegation.

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
| Lambda reserved concurrency exceeds available quota | Pull the latest scripts and rerun setup. The quota preflight logs insufficient capacity and deploys capture functions using the shared pool while retaining requested YAML settings. |
| Local preview cannot find Lambda ZIPs | Use `build.bat` / `build.sh`, or the wizard/deploy tool which builds automatically. |
| Partial application deployment | Keep state and fix the reported prerequisite; rerun deployment. |

The wizard does not request Lambda quota increases, leave the SES sandbox, provision website hosting or rotate existing proof keys automatically. Cloudflare DNS changes are applied through the DNS stage or dedicated menu option; manual DNS setup remains available.

## Remove an environment

Use `teardown.bat` or `./teardown.sh` for a reviewed, resumable teardown of the selected stack, GitHub environment, deployment role, and SES Cloudflare records. See [teardown and recovery](teardown.md). The state bucket and shared OIDC provider are retained.

## Ordered first deployment and DNS verification

After configuration, the setup wizard automatically performs these stages in order. The recovery menu also offers **Build, preview and deploy**:

1. Build Lambda archives, then preview and apply a targeted Pulumi update for the SES identity and DKIM configuration only. If `route53ZoneId` is configured, its SES verification records are included. Other application resources are retained, not removed or deployed by this stage.
2. If SES is not already verified, publish DNS using the provider already selected. Cloudflare setup runs automatically at this point; Route 53 records are managed by the targeted Pulumi update and do not prompt for a Cloudflare token.
3. Poll AWS every 15 seconds until both identity verification and DKIM report `Success`. Pending verification blocks the full deployment. Ctrl+C interrupts this wait and returns to the menu; resources are retained. Failed verification or an AWS read error stops the sequence with an explanation.
4. Preview and apply the full application deployment. Both Pulumi updates run automatically after preview, without another deployment confirmation.

Rerun the same option to resume. The wizard reads actual Pulumi/AWS state; an already verified identity skips DNS configuration and waiting. DNS propagation can take time, so pausing does not require teardown or a new stack. Builds still run before targeted updates because Pulumi evaluates the entire program, including local Lambda archive references.

GitHub Actions deploy checks SES readiness first and gives instructions to complete setup if the identity is missing or unverified. Preview-only remains available. Complete this initial staged setup locally before deploying subsequent changes through Actions. The state backend and stack are shared by both paths.

## Automatic setup and resume memory

Full setup continues automatically from configuration through IAM/GitHub setup, SES prerequisites, DNS publication, verification polling and complete application deployment. It displays plans before applying them without repeated deployment approvals. On success it prints the API/Cognito values needed by Android and exits. If a phase fails, successful work is retained and the recovery menu remains available. The standalone GitHub-only setup keeps its existing confirmations. Evidence of missing previous stack state stops setup for migration/restoration; existing encryption metadata is preserved.

The ignored `bootstrap.<environment>.local.json` now remembers non-secret configuration choices, AWS authentication mode/profile, account and deployment role, application domains, DNS choices, and the latest completed deployment phase. Writes use a temporary file and a recovery backup; `.tmp` and `.bak` files are also ignored. A backup is read if replacement was interrupted. Keep these files private and run only one setup instance for an environment at a time.

On rerun, choose the repository/environment and supply credentials. Existing GitHub values take precedence over checkpoint defaults; current Pulumi application configuration takes precedence over remembered answers. Previously answered configuration prompts are skipped. No tokens, AWS keys, passphrases or proof keys are stored in this memory file. Without an unlocked vault, GitHub tokens, the stack passphrase and any required Cloudflare token are requested again; AWS SSO/login may need to authenticate again.

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

The prerequisite update requests an ACM certificate in the API's region alongside the SES resources. Before deploying the complete application, the wizard publishes the ACM validation CNAME through Cloudflare and polls until ACM reports `ISSUED`. It then creates the regional API Gateway custom domain and root API mapping, and publishes a DNS-only CNAME pointing to the **custom-domain target**, not the raw API URL. An existing Cloudflare token from SES setup is reused in memory and can also be retained in the optional encrypted vault. Matching records are reused; conflicting records stop setup without replacement. Certificate validation records must remain for renewal. Keep CNAME flattening disabled for validation records.

If `route53ZoneId` is configured, Pulumi manages both the certificate-validation record and API CNAME in that zone. Cloudflare is not used for those records.

The `apiUrl` output becomes `https://<apiDomain>`, retaining existing API route paths. `rawApiUrl` retains the AWS-generated endpoint for diagnostics and compatibility. The raw endpoint remains enabled; both use the same API authentication. `apiCertificateArn`, `apiDomain`, and `apiDomainTarget` are also exported. Without an `apiDomain` setting, direct Pulumi deployment retains the original raw URL behavior.

GitHub deployment checks the configured certificate is issued before proceeding. First-time certificate validation and Cloudflare publication run through local setup; if DNS publication fails after AWS deployment, rerun setup to finish it. DNS propagation can delay client access after successful publication. This does not create website hosting or change Cognito IDs, and Android still needs its base URL configured from `apiUrl`.

## Lambda build performance

The Go packager builds independent Lambda entry points concurrently with up to four workers (reduced on smaller machines). Go package parallelism is divided among workers. All six `auth-*` archives reuse one compiled `cmd/signin` binary: 15 Lambda archives require only 10 unique builds. A compilation failure cancels running compiler processes, stops remaining work, and prevents deployment.

Within one staged setup run, the full deployment reuses the archives built for the prerequisite phase and checks that all archives are still present. A new setup attempt builds again through Go's build cache, so changed sources are picked up. If you edit code while the wizard is waiting for DNS, restart setup to include those edits; the current run uses its earlier build. Both partial and full deployments wait until compilation and packaging have finished.

## Shared environment index

Full setup now also provisions or reuses the separate `attestra-index/shared` project before deploying the application. Keep its original Pulumi passphrase, and review/commit `infra-index/Pulumi.shared.yaml` alongside the environment YAML. The shared index and its state remain after environment teardown. Read [environment-index.md](environment-index.md) for the public configuration contract, concurrency rules, and publication recovery commands.
