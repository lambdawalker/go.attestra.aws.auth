# Remove the shared environment index

Use this only when retiring the shared index itself. Normal `teardown.bat` /
`teardown.sh` removes one environment's registry entry and retains the index.

First tear down every environment with the normal environment teardown wizard.
Stop local deployments and GitHub deployment workflows until index teardown
finishes. An empty public list alone is insufficient: unpublished deployments
and retired environments can still hold locks in the registry.

From the repository root:

```powershell
.\teardown-index.bat
```

```sh
./teardown-index.sh
```

Or run `go -C tools/deploy run . -teardown-index`. There is no unattended destroy
flag. The wizard asks for the AWS region, optional environment vault to unlock,
AWS administrator authentication (SSO, browser login, or access keys), and the
**shared index** Pulumi passphrase. This can differ from an environment's
passphrase. GitHub credentials are not required; this command does not delete
GitHub environments or their deployment roles.

The backend is the same deterministic account/region bucket used by setup:
`s3://attestra-index-state-<account>-<region>`, project `attestra-index`, stack
`shared`. The wizard verifies bucket ownership and rejects conflicting provider
configuration, unknown protected resources, and changed resources on resume.

It then:

1. Acquires the same S3 bootstrap lock as index setup.
2. Reads every page of the DynamoDB registry consistently. All rows must be
   retired tombstones with no published entry or active deployment/teardown lock.
   An AWS access error never counts as an empty registry.
3. Captures the resource inventory and exact index DNS records. Route 53 records
   are removed through Pulumi. Cloudflare records require Zone Read/DNS Edit and
   are matched by record ID, name, type, and content before deletion. Check that
   the ACM validation record is not reused by another certificate for the same
   hostname.
4. Shows the account, region, domain, table, resources, and DNS records, then asks
   you to type `delete index <account> <region>`; the exact text is displayed.
5. Records a persistent teardown marker, rechecks the registry, unprotects only
   the captured registry table, previews destruction, and destroys the stack.
6. Removes the approved Cloudflare records, verifies the captured AWS resources
   and DNS records are absent, removes the empty stack, and verifies stack removal.
7. Clears the bootstrap-ready and teardown-pending markers and backs up the
   encrypted shared YAML under `.attestra/` before removing the active YAML.

Progress is private local metadata in
`.attestra/teardown-index.<account>.<region>.local.json`. On failure, keep that
file and rerun the same command. It retains identifiers needed to verify
resources already removed by a partial run. The remote pending marker prevents
setup from recreating infrastructure before cleanup finishes. Another computer
needs the original checkpoint to resume. If a process is forcibly killed, inspect
and clear its stale local/S3 bootstrap lock only after confirming it has stopped.

A subsequent setup archives a completed index-teardown checkpoint automatically
before starting a new lifecycle. This prevents the old completed flag from
blocking teardown of a recreated index.

The state bucket and its history, credential vaults, GitHub configuration, OIDC
provider, Cloudflare zone, unrelated DNS, and service-created logs/backups are
retained. These are reported as retained, not claimed as deleted. Use normal
environment teardown for any remaining environment-specific configuration.

The setup lock does not suspend arbitrary AWS API callers or already-running
CI deployments. Keep deployment processes stopped throughout this operation;
registry scans are preflight checks, not a global transactional deployment freeze.
