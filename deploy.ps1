# Windows launcher for the Go deployment tool.
[CmdletBinding()]
param(
    [string]$Stack = 'dev',
    [string]$Backend,
    [string]$Region = 'us-east-2',
    [string]$MigrateFrom,
    [switch]$Pull
)
$ErrorActionPreference = 'Stop'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Install Go 1.26.6+ and add it to PATH.'
}
$arguments = @('-repo-root', $PSScriptRoot, '-stack', $Stack, '-region', $Region)
if ($Backend) { $arguments += @('-backend', $Backend) }
if ($MigrateFrom) { $arguments += @('-migrate-from', $MigrateFrom) }
if ($Pull) { $arguments += '-pull' }
& go -C (Join-Path $PSScriptRoot 'tools/deploy') run . @arguments
exit $LASTEXITCODE
