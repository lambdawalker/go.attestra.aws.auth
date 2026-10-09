# Offline wrapper checks. No credentials, build, or deployment.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$temp = Join-Path ([IO.Path]::GetTempPath()) ('attestra deploy ' + [guid]::NewGuid())
$previousPath = $env:PATH
$previousTrace = $env:DEPLOY_TEST_TRACE
try {
    New-Item -ItemType Directory -Path $temp -Force | Out-Null
    Copy-Item (Join-Path $root 'deploy.ps1'), (Join-Path $root 'deploy.bat') $temp
    $env:DEPLOY_TEST_TRACE = Join-Path $temp 'trace.txt'
    @'
@echo off
> "%DEPLOY_TEST_TRACE%" echo %*
exit /b 23
'@ | Set-Content (Join-Path $temp 'go.cmd') -Encoding ascii
    $env:PATH = "$temp;$previousPath"
    & (Join-Path $temp 'deploy.bat') -Stack stage -Backend s3://test-state -MigrateFrom owner/attestra-auth-email/stage -Pull
    if ($LASTEXITCODE -ne 23) { throw "Exit code was not forwarded: $LASTEXITCODE" }
    $trace = Get-Content $env:DEPLOY_TEST_TRACE -Raw
    foreach ($expected in @('tools\deploy', '-repo-root', '-stack stage', '-backend s3://test-state', '-migrate-from owner/attestra-auth-email/stage', '-pull')) {
        if (-not $trace.Contains($expected)) { throw "Missing forwarded argument: $expected" }
    }
    if (-not $trace.Contains($temp)) { throw 'Repository path with spaces was not forwarded.' }
    Write-Host 'Deployment launcher checks passed.'
} finally {
    $env:PATH = $previousPath
    $env:DEPLOY_TEST_TRACE = $previousTrace
    Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue
}
$global:LASTEXITCODE = 0
