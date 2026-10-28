# Attestra environment tools

This Go module contains the setup, deployment, credential management, health-check, Android export, and teardown commands. Run them through the repository's `.bat` / `.sh` launchers or the existing entry point:

```sh
go -C tools/deploy run . -bootstrap
go -C tools/deploy run . -teardown
go -C tools/deploy run . -teardown-index
go -C tools/deploy run . -manage-credentials
```

The launchers supply the repository path explicitly. Direct commands retain the existing `-repo-root` option and default. `main.go` only calls the workflow entry point and handles process exit. GitHub Actions uses the same executable.

## Package map

All paths below are relative to this directory.

| Directory | Responsibility |
| --- | --- |
| `internal/workflow` | CLI dispatch, setup/deploy/teardown sequencing, checkpoints, AWS login renewal, capacity policy, health checks, and deployment summaries |
| `internal/aws` | AWS CLI transport, redacted errors, and subprocess environment filtering |
| `internal/build` | Parallel Lambda compilation, archive packaging, and archive checks |
| `internal/dns` | Cloudflare API access, DNS planning, ownership comparisons, and approved record deletion |
| `internal/github` | GitHub environments, encrypted secret submission, and deployment/sharing checks |
| `internal/vault` | Credential encryption, file format, passphrase validation/rotation, and key-buffer cleanup |
| `internal/registry` | Signed environment-index requests, credential-provider refresh, and request retries |
| `internal/android` | Public Android properties-file serialization |
| `internal/ui` | Shared terminal prompts, environment picker, and environment-name validation |

Workflow code composes these packages. Reusable packages must not import `workflow`; supply data or a narrow callback/interface instead. For example, AWS CLI transport accepts a renewal-error callback, while the workflow owns interactive authentication. Route53 configuration remains in workflow because it is applied through the Pulumi deployment stages. Index infrastructure setup and deployment receipts likewise remain with their workflows.

Keep unit tests beside their implementations. Tests that exercise several packages together belong in `internal/workflow`. Launcher tests stay at the module root so they continue testing the real repository launchers.

## Verification

From the repository root:

```sh
go -C tools/deploy test -race ./...
go -C tools/deploy vet ./...
go -C tools/deploy build -o attestra-deploy ./
```

Use `./...` when testing: the command package alone does not include the internal package tests. Tests use local fixtures and fake services; they do not deploy or tear down cloud environments.

The package layout does not change CLI flags, checkpoint JSON, vault encryption or location, Pulumi configuration, Android export keys, or the GitHub workflow entry point. See [deployment instructions](../../docs/deployment.md), [first deployment](../../docs/first-deployment.md), and [teardown](../../docs/teardown.md).

Windows credential renewal uses an unquoted absolute helper path because the AWS Go SDK's `cmd.exe` invocation does not preserve quoted executable paths. Paths containing spaces use the Windows short-path alias. If that filesystem has short names disabled, the wizard stops with instructions to set `TEMP` and `TMP` to an existing path without spaces (for example, `C:\attestra-temp`) and rerun. If `GOTMPDIR` is set, update it too; for a prebuilt executable, move it to such a directory. It never changes the original AWS profile.

## Generated local state

Wizard progress files live in ignored `.attestra/` at the repository root:

- `bootstrap.<environment>.local.json`: non-secret setup choices, progress, health/completion receipts and configuration fingerprints.
- `teardown.<environment>.local.json`: teardown progress, resource inventory and deletion verification.
- `index-publication.<environment>.local.json`: pending publication/recovery receipts.
- `teardown-history/`: archived completed teardown records and their encrypted configuration backups.

Their `.bak`, `.tmp` and `.lock` siblings stay alongside them. Stop any running setup/deployment/teardown before upgrading. On the next command, old root-level files are moved intact. Conflicting old/new files or existing operation locks stop migration for inspection; no history is overwritten. Interrupted moves resume on rerun.

Keep `.attestra/` when resuming interrupted operations. These files contain no credential values but include infrastructure identifiers and recovery metadata; keep them private. The optional encrypted credential vault stays in its existing user-specific location. Build artifacts already live in `dist/`, public Android exports in `android-config/`, and Pulumi configuration stays in `infra/` and `infra-index/`.

Shared-index teardown has its own [runbook](../../docs/teardown-index.md) and private `.attestra/teardown-index.<account>.<region>.local.json` checkpoint. It does not replace environment teardown.

## Export values for the modular deployment wizard

From the repository root, run `./export-wizard.sh --environment dev`, or on
Windows `export-wizard.bat --environment dev`. Requires Python 3 and PyYAML
(`python -m pip install -r scripts/requirements-export.txt`).
It reads local `infra/Pulumi.<environment>.yaml` and
`infra-index/Pulumi.shared.yaml` without contacting a state backend or decrypting
secrets, plus `.attestra/deploy.<environment>.local.json` when available.
If a local stack file is absent, it falls back to read-only
`pulumi config --json --non-interactive` using the current Pulumi backend/session.
A failed fallback reports Pulumi stderr and recovery guidance instead of a traceback. Run once per environment; exports merge into
`~/.attestra/legacy-wizard.json` with private file permissions.

Exports include sender domain/address, AWS region/account when available,
base domain derived from the index domain, Android associations, environment
class, and saved profile/state base. The new wizard automatically skips questions
for these values. Missing settings still prompt. Flags and local wizard settings
win over exports. Legacy domains, certificate ownership, secrets and credential
vaults are excluded. This does not transfer infrastructure ownership or deploy.
Use `--output PATH` only to inspect/export elsewhere; the new wizard discovers
the default home-directory path automatically.
