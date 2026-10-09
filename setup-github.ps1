# Configure AWS OIDC/deployment IAM and the GitHub dev environment.
$ErrorActionPreference = 'Stop'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Install Go 1.26.6+ and add it to PATH.'
}
& go -C (Join-Path $PSScriptRoot 'tools/deploy') run . -setup-github
exit $LASTEXITCODE
