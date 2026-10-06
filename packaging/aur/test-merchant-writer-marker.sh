#!/usr/bin/env bash
# shellcheck disable=SC2030,SC2031,SC2034 # PKGBUILD variables belong to isolated fixture shells.
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly RECIPE="$HERE/lmm-api-go-bin"
readonly SHARED="$HERE/../common/lmm-api"

die() {
  printf 'test-merchant-writer-marker: %s\n' "$*" >&2
  exit 1
}

: "${TMPDIR:?set TMPDIR to a marker-owned fixture workspace}"
tmp=$(mktemp -d "$TMPDIR/lmm-merchant-marker.XXXXXXXX")
cleanup() { rm -rf -- "$tmp"; }
trap cleanup EXIT

run_fixture() {
  local label=$1 architecture=$2 bytes=$3 expected=$4 kind=${5:-file}
  local work="$tmp/$label" release_arch artifact bundle archive installed
  case "$architecture" in
    x86_64) release_arch=amd64 ;;
    aarch64) release_arch=arm64 ;;
    *) die "unknown fixture architecture: $architecture" ;;
  esac
  artifact="lmm-api-go-$(sed -n 's/^pkgver=//p' "$RECIPE/PKGBUILD")-linux-$release_arch"
  bundle="$work/stage/$artifact"
  archive="$work/src/$artifact.tar.gz"
  installed="$work/pkg/usr/share/doc/lmm-api-go-bin/MERCHANT_STORE_WRITER_CAPABILITY"
  mkdir -p "$bundle/edge-policy" "$work/src" "$work/pkg"
  printf '#!/bin/sh\nexit 0\n' >"$bundle/lmm-api-go"
  chmod 0755 "$bundle/lmm-api-go"
  cp "$SHARED/lmm-api.service" "$SHARED/lmm-api-go.env" \
    "$SHARED/lmm-api-memory.conf" "$SHARED/lmm-api-operator.sysusers" \
    "$SHARED/lmm-api-operator.tmpfiles" "$SHARED/lmm-api-operator.sudoers" "$bundle/"
  printf '%064d\n' 0 >"$bundle/API_ROUTE_CONTRACT_REVISION"
  printf '%040d\n' 0 >"$bundle/REVISION"
  for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
    printf 'local package fixture\n' >"$bundle/$file"
  done
  printf 'local edge-policy fixture\n' >"$bundle/edge-policy/fixture.conf"
  case "$kind" in
    file) printf '%b' "$bytes" >"$bundle/MERCHANT_STORE_WRITER_CAPABILITY" ;;
    absent) ;;
    symlink)
      printf '%b' "$bytes" >"$bundle/marker-target"
      ln -s marker-target "$bundle/MERCHANT_STORE_WRITER_CAPABILITY"
      ;;
    broken-symlink) ln -s missing-marker-target "$bundle/MERCHANT_STORE_WRITER_CAPABILITY" ;;
    *) die "unknown fixture marker kind: $kind" ;;
  esac
  tar -czf "$archive" -C "$work/stage" "$artifact"
  (cd "$work/src" && sha256sum "$artifact.tar.gz" >"$artifact.tar.gz.sha256")
  printf 'local signature-verifier fixture\n' >"$archive.sigstore.json"

  # Run the real recipe's SHA check, extraction, prepare and package functions.
  # Only network signature verification is a fixture: it must receive the
  # existing exact repository/workflow/tag identity and fixed GitHub issuer.
  if FIXTURE_RECIPE="$RECIPE" FIXTURE_WORK="$work" FIXTURE_ARCH="$architecture" \
    bash -Eeuo pipefail -c '
      startdir=$FIXTURE_RECIPE
      srcdir=$FIXTURE_WORK/src
      pkgdir=$FIXTURE_WORK/pkg
      CARCH=$FIXTURE_ARCH
      cosign() {
        local archive="${_artifact}-${_release_arch}.tar.gz"
        [[ $# == 8 && $1 == verify-blob && $2 == --bundle && $3 == "$archive.sigstore.json" ]]
        [[ $4 == --certificate-identity && $5 == "$url/.github/workflows/release-go.yml@refs/tags/$_release_tag" ]]
        [[ $6 == --certificate-oidc-issuer && $7 == https://token.actions.githubusercontent.com && $8 == "$archive" ]]
        printf "exact signature inputs checked\n" >"$FIXTURE_WORK/verifier-inputs"
      }
      source "$startdir/PKGBUILD"
      cd "$srcdir"
      prepare
      package
    ' >"$work/recipe.log" 2>&1; then
    [[ $expected == pass ]] || die "$label accepted an invalid marker"
    [[ -f $work/verifier-inputs ]] || die "$label skipped the signature verifier fixture"
    if [[ $kind == absent ]]; then
      [[ ! -e $installed && ! -L $installed ]] || die "$label fabricated a legacy capability"
    else
      [[ -f $installed && ! -L $installed ]] || die "$label did not install a regular marker"
      cmp -s "$bundle/MERCHANT_STORE_WRITER_CAPABILITY" "$installed" || die "$label changed signed marker bytes"
      [[ $(stat -c %a "$installed") == 644 ]] || die "$label installed an unsafe marker mode"
    fi
    [[ ! -e $work/pkg/usr/bin/lmm-api && ! -L $work/pkg/usr/bin/lmm-api ]] || die "$label reintroduced the generic provider payload"
  else
    [[ $expected == fail ]] || { cat "$work/recipe.log" >&2; die "$label rejected a valid fixture"; }
    [[ ! -e $installed && ! -L $installed ]] || die "$label installed a rejected marker"
  fi
  printf 'PASS %s\n' "$label"
}

run_fixture amd64-current x86_64 '4\n' pass
run_fixture arm64-current aarch64 '4\n' pass
run_fixture amd64-first-capability x86_64 '1\n' pass
run_fixture historical-absence x86_64 '' pass absent
run_fixture no-newline x86_64 '4' fail
run_fixture extra-newline x86_64 '4\n\n' fail
run_fixture nul-byte x86_64 '4\x00\n' fail
run_fixture leading-space x86_64 ' 4\n' fail
run_fixture multi-digit x86_64 '44\n' fail
run_fixture zero-capability x86_64 '0\n' fail
run_fixture live-symlink x86_64 '4\n' fail symlink
run_fixture broken-symlink x86_64 '' fail broken-symlink
