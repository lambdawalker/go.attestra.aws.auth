# Offline orchestration tests. No real AWS, Git, Go or Pulumi commands are invoked.
$ErrorActionPreference = 'Stop'
$fixture = Join-Path ([System.IO.Path]::GetTempPath()) ('attestra deploy ' + [guid]::NewGuid())
$originalProfile = $env:AWS_PROFILE
$originalLocation = (Get-Location).Path
New-Item -ItemType Directory -Path (Join-Path $fixture 'infra') -Force | Out-Null
Copy-Item (Join-Path $PSScriptRoot '../deploy.ps1') (Join-Path $fixture 'deploy.ps1')
Set-Content (Join-Path $fixture 'build.ps1') 'Register-DeployCall ''build'''

function Register-DeployCall([string]$Value) {
    $global:DeployTestCalls.Add($Value)
    $global:LASTEXITCODE = if ($Value -eq $global:DeployTestFailure) { 1 } else { 0 }
}
function git { Register-DeployCall ('git ' + ($args -join ' ')) }
function go { throw 'Unexpected real build invocation' }
function aws { Register-DeployCall ('aws ' + ($args -join ' ')) }
function pulumi {
    Register-DeployCall ('pulumi ' + ($args -join ' '))
    if ($args[0] -eq 'config' -and $global:LASTEXITCODE -eq 0) { $global:DeployTestConfig }
}
function Run-Deployment([string]$Failure = '', [hashtable]$Options = @{}) {
    $global:DeployTestCalls = [System.Collections.Generic.List[string]]::new()
    $global:DeployTestFailure = $Failure
    $env:AWS_PROFILE = 'shell-profile'
    $caught = $false
    try { & (Join-Path $fixture 'deploy.ps1') @Options }
    catch { $caught = $true }
    if ((Get-Location).Path -ne $originalLocation) { throw 'Deployment did not restore working directory.' }
    if ($env:AWS_PROFILE -ne 'shell-profile') { throw 'Deployment did not restore AWS_PROFILE.' }
    return $caught
}
try {
    $global:DeployTestConfig = '{"aws:profile":{"value":"stack-profile"}}'
    $expected = @(
        'git status --porcelain', 'git pull --ff-only',
        'pulumi config --json --stack dev',
        'aws login --profile stack-profile',
        'aws sts get-caller-identity --profile stack-profile --no-cli-pager',
        'build', 'pulumi preview --stack dev', 'pulumi up --stack dev'
    )
    if (Run-Deployment) { throw 'Successful deployment unexpectedly failed.' }
    if (($global:DeployTestCalls -join '|') -ne ($expected -join '|')) { throw 'Unexpected deployment order or automatic approval flag.' }

    foreach ($failure in $expected) {
        if (-not (Run-Deployment -Failure $failure)) { throw "Failure was ignored: $failure" }
        if ($global:DeployTestCalls[$global:DeployTestCalls.Count - 1] -ne $failure) {
            throw "Deployment continued after failure: $failure"
        }
    }
    if (Run-Deployment -Options @{ Sso = $true }) { throw 'SSO login failed.' }
    if (-not $global:DeployTestCalls.Contains('aws sso login --profile stack-profile')) { throw 'SSO command missing.' }
    if (-not (Run-Deployment -Options @{ Profile = 'wrong-profile' })) { throw 'Mismatched provider profile accepted.' }
    if ($global:DeployTestCalls -match '^aws ') { throw 'Login attempted with a mismatched profile.' }
    $global:DeployTestConfig = '{}'
    if (Run-Deployment) { throw 'Environment profile fallback failed.' }
    if (-not $global:DeployTestCalls.Contains('aws login --profile shell-profile')) { throw 'Environment profile was ignored.' }
    # Exercise the batch wrapper against a harmless child script, including spaces and failure status.
    Copy-Item (Join-Path $PSScriptRoot '../deploy.bat') (Join-Path $fixture 'deploy.bat')
    Set-Content (Join-Path $fixture 'deploy.ps1') 'param([string]$Stack, [string]$Profile); Write-Output "$Stack|$Profile"; exit 23'
    $wrapperOutput = & (Join-Path $fixture 'deploy.bat') -Stack 'test stack' -Profile 'test-profile'
    if ($LASTEXITCODE -ne 23) { throw 'Batch wrapper did not preserve the exit code.' }
    if (($wrapperOutput -join "`n") -ne 'test stack|test-profile') { throw 'Batch wrapper did not preserve arguments.' }
    Write-Host 'Deployment orchestration tests passed.'  -ForegroundColor Green
} finally {
    $env:AWS_PROFILE = $originalProfile
    Set-Location $originalLocation
    Remove-Item $fixture -Recurse -Force
    Remove-Variable DeployTestCalls, DeployTestFailure, DeployTestConfig -Scope Global -ErrorAction SilentlyContinue
}
# The expected batch failure above must not become the test runner's exit status.
$global:LASTEXITCODE = 0
