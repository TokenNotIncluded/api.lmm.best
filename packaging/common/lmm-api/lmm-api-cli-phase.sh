# shellcheck shell=bash disable=SC2034
# Canonical package metadata/install contract for the provider-executable layout.
#
# Provider packages coexist. They install only their real provider executable;
# /usr/bin/lmm-api is runtime state managed by `lmm-api backend select`.

readonly LMM_GO_PROVIDER_EXECUTABLE=lmm-api-go
readonly LMM_GO_LEGACY_MIGRATION_VERSION=0.1.69

lmm_go_provider_apply_metadata() {
  local version=$1 current_package=$2
  shift 2
  provides=("lmm-api-go=${version}")
  conflicts=()
  replaces=()
  local variant
  for variant in "$@"; do
    [[ $variant == lmm-api-go || $variant == lmm-api-go-bin || $variant == lmm-api-go-git ]] || return 1
    [[ $variant == "$current_package" ]] || conflicts+=("$variant")
  done
}

lmm_go_provider_assert_payload() {
  local pkgdir=$1 provider="$1/usr/bin/$LMM_GO_PROVIDER_EXECUTABLE"
  [[ -f $provider && ! -L $provider && -x $provider ]] || return 1
  [[ ! -e $pkgdir/usr/bin/lmm-api && ! -L $pkgdir/usr/bin/lmm-api ]] || return 1
}

lmm_go_provider_is_verified_legacy_release() {
  [[ $1 == "$LMM_GO_LEGACY_MIGRATION_VERSION" ]]
}
