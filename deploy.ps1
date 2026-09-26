# Pull the current branch, package every Lambda, and update the selected Pulumi stack.
param(
    [string]$Stack = 'dev'
)

$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    foreach ($command in @('git', 'go', 'pulumi')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw "Required command '$command' was not found on PATH."
        }
    }

    $changes = & git status --porcelain
    if ($LASTEXITCODE -ne 0) { throw 'Could not read Git working tree status.' }
    if ($changes) { throw 'Commit or stash working tree changes before deploying.' }

    & git pull --ff-only
    if ($LASTEXITCODE -ne 0) { throw 'Git pull failed. The Lambdas were not built or deployed.' }

    & (Join-Path $PSScriptRoot 'build.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Lambda build failed. Pulumi was not started.' }

    Push-Location (Join-Path $PSScriptRoot 'infra')
    try {
        & pulumi up --stack $Stack
        if ($LASTEXITCODE -ne 0) { throw "Pulumi update failed for stack '$Stack'." }
    } finally {
        Pop-Location
    }
} finally {
    Pop-Location
}
