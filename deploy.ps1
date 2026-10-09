# Pull, log in to AWS, build all Lambdas, preview, then update the selected stack.
[CmdletBinding()]
param(
    [ValidateNotNullOrEmpty()]
    [string]$Stack = 'dev',
    [string]$Profile,
    [switch]$Sso
)

$ErrorActionPreference = 'Stop'
$previousProfile = $env:AWS_PROFILE
Push-Location $PSScriptRoot
try {
    foreach ($command in @('git', 'aws', 'go', 'pulumi')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw "Required command '$command' was not found on PATH."
        }
    }

    $changes = & git status --porcelain
    if ($LASTEXITCODE -ne 0) { throw 'Could not read Git working tree status.' }
    if ($changes) { throw 'Commit or stash working tree changes before deploying.' }

    & git pull --ff-only
    if ($LASTEXITCODE -ne 0) { throw 'Git pull failed. The Lambdas were not built or deployed.' }

    Push-Location (Join-Path $PSScriptRoot 'infra')
    try {
        # Match login to the provider profile, which takes precedence over AWS_PROFILE.
        $configuration = & pulumi config --json --stack $Stack
        if ($LASTEXITCODE -ne 0) { throw "Could not read configuration for stack '$Stack'." }
        $stackConfig = ($configuration -join "`n") | ConvertFrom-Json
        $stackProfile = $stackConfig.'aws:profile'.value
        if ($Profile -and $stackProfile -and $Profile -ne $stackProfile) {
            throw "Profile '$Profile' differs from stack aws:profile '$stackProfile'. Use the stack profile or change its configuration first."
        }
        if (-not $Profile) { $Profile = $stackProfile }
        if (-not $Profile) { $Profile = $env:AWS_PROFILE }
        if (-not $Profile) { $Profile = 'default' }
        $env:AWS_PROFILE = $Profile

        Write-Host "Logging in to AWS profile '$Profile'..." -ForegroundColor Cyan
        if ($Sso) { & aws sso login --profile $Profile }
        else { & aws login --profile $Profile }
        if ($LASTEXITCODE -ne 0) { throw 'AWS login failed. Nothing was built or deployed.' }

        Write-Host 'Checking AWS identity...' -ForegroundColor Cyan
        & aws sts get-caller-identity --profile $Profile --no-cli-pager
        if ($LASTEXITCODE -ne 0) { throw 'AWS identity check failed. Nothing was built or deployed.' }
    } finally {
        Pop-Location
    }

    Write-Host 'Building Lambda archives...' -ForegroundColor Cyan
    & (Join-Path $PSScriptRoot 'build.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Lambda build failed. Pulumi was not started.' }

    Push-Location (Join-Path $PSScriptRoot 'infra')
    try {
        Write-Host "Previewing stack '$Stack'..." -ForegroundColor Cyan
        & pulumi preview --stack $Stack
        if ($LASTEXITCODE -ne 0) { throw "Pulumi preview failed for stack '$Stack'. Update was not started." }

        Write-Host "Updating stack '$Stack'..." -ForegroundColor Cyan
        # Keep Pulumi's normal confirmation; never approve changes automatically.
        & pulumi up --stack $Stack
        if ($LASTEXITCODE -ne 0) { throw "Pulumi update failed for stack '$Stack'." }
    } finally {
        Pop-Location
    }
} finally {
    $env:AWS_PROFILE = $previousProfile
    Pop-Location
}
