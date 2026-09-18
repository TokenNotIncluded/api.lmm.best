$ErrorActionPreference = 'Stop'

if (-not (Get-Command pi -ErrorAction SilentlyContinue)) {
  Write-Error 'pi is not installed. Install Pi first, then run this script again.'
  exit 1
}

& pi install 'git:github.com/TokenNotIncluded/pi-lmm-provider'
exit $LASTEXITCODE
