$ErrorActionPreference = 'Stop'

if (Get-Command dsh -ErrorAction SilentlyContinue) {
  & dsh web @args
  exit $LASTEXITCODE
}

Write-Error 'dsh is not installed. Install DeepSeek Harness, then run this script again.'
