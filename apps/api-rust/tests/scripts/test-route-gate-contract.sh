#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)
# shellcheck source=route-gate-fixture-lib.sh
# shellcheck disable=SC1091 # Repository root is resolved at runtime.
source "$repo/apps/api-rust/tests/scripts/route-gate-fixture-lib.sh"
validator="$repo/packaging/common/lmm-api/validate-route-gate"
current_gate="$repo/apps/api-rust/tests/fixtures/routes/route-gate.tsv"
frozen="$repo/apps/api-rust/tests/fixtures/routes/frozen-route-auth.tsv"
runtime=$(mktemp -d /tmp/lmm-route-gate-contract.XXXXXXXX)
trap 'rm -rf -- "$runtime"' EXIT

fail() { printf 'route-gate-contract: %s\n' "$*" >&2; exit 1; }
expect_fail() {
  if "$@" >"$runtime/out" 2>"$runtime/err"; then
    fail "expected failure: $*"
  fi
}
validator_run() {
  local snapshot manifest manifest_sha rc gate_path='' frozen_path='' migration_path
  local index
  migration_path="$repo/packaging/common/lmm-api/migration-compatibility.env"
  for ((index=1; index <= $#; index++)); do
    case ${!index} in
      --gate) ((index += 1)); gate_path=${!index} ;;
      --frozen-contract) ((index += 1)); frozen_path=${!index} ;;
      --migration-compatibility) ((index += 1)); migration_path=${!index} ;;
    esac
  done
  [[ -n $gate_path && -n $frozen_path ]] || fail 'validator fixture is missing gate/frozen paths'
  snapshot=$(mktemp -d "$runtime/validator-snapshot.XXXXXXXX")
  manifest=$(mktemp "$runtime/validator-assets.XXXXXXXX")
  {
    printf '%s  route-gate.tsv\n' "$(sha256sum "$gate_path" | awk '{print $1}')"
    printf '%s  validate-route-gate\n' "$(sha256sum "$validator" | awk '{print $1}')"
    printf '%s  migration-compatibility.env\n' "$(sha256sum "$migration_path" | awk '{print $1}')"
    printf '%s  frozen-route-auth.tsv\n' "$(sha256sum "$frozen_path" | awk '{print $1}')"
  } >"$manifest"
  manifest_sha=$(sha256sum "$manifest" | awk '{print $1}')
  set +e
  LMM_ROUTE_GATE_TEST_MODE=1 "$validator" --snapshot-dir "$snapshot" \
    --assets-manifest "$manifest" --assets-manifest-sha256 "$manifest_sha" "$@"
  rc=$?
  set -e
  rm -rf -- "$snapshot" "$manifest"
  return "$rc"
}
frozen_sha=$(sha256sum "$frozen" | awk '{print $1}')
revision=$(git -C "$repo" rev-parse HEAD)
source_args=(--mode source --frozen-contract "$frozen" --frozen-contract-sha256 "$frozen_sha" \
  --evidence-root "$repo" --revision "$revision")

bash -n "$validator" "$repo/apps/api-rust/tests/scripts/route-gate-fixture-lib.sh"
validator_run "${source_args[@]}" --gate "$current_gate"
expect_fail validator_run --mode activate --gate "$current_gate" --frozen-contract "$frozen" \
  --frozen-contract-sha256 "$frozen_sha" --evidence-root "$repo" --revision "$revision" \
  --migration-compatibility "$repo/packaging/common/lmm-api/migration-compatibility.env"

synthetic="$runtime/synthetic"
route_gate_fixture_create "$repo" "$synthetic" "$revision"
synthetic_frozen_sha=$(sha256sum "$synthetic/frozen-route-auth.tsv" | awk '{print $1}')
validator_run --mode source --gate "$synthetic/route-gate.tsv" \
  --frozen-contract "$synthetic/frozen-route-auth.tsv" --frozen-contract-sha256 "$synthetic_frozen_sha" \
  --evidence-root "$synthetic" --revision "$revision"
validator_run --mode activate --gate "$synthetic/route-gate.tsv" \
  --frozen-contract "$synthetic/frozen-route-auth.tsv" --frozen-contract-sha256 "$synthetic_frozen_sha" \
  --evidence-root "$synthetic" --revision "$revision" \
  --migration-compatibility "$synthetic/migration-compatibility.env"

first_verified=$(sed -n '2p' "$synthetic/route-gate.tsv")
single_gate="$runtime/single-route.tsv"
awk -v replacement="$first_verified" 'NR == 2 { print replacement; next } { print }' \
  "$current_gate" >"$single_gate"

make_route_case() {
  local name=$1 kind=$2 filter=$3
  local case_dir="$runtime/$name" file old_sha new_sha
  mkdir -p "$case_dir/evidence"
  cp "$single_gate" "$case_dir/route-gate.tsv"
  cp "$synthetic/evidence/route-1-"*.json "$case_dir/evidence/"
  file="$case_dir/evidence/route-1-$kind.json"
  old_sha=$(sha256sum "$file" | awk '{print $1}')
  jq "$filter" "$file" >"$file.new"
  mv "$file.new" "$file"
  new_sha=$(sha256sum "$file" | awk '{print $1}')
  sed "s/$old_sha/$new_sha/" "$case_dir/route-gate.tsv" >"$case_dir/gate.new"
  mv "$case_dir/gate.new" "$case_dir/route-gate.tsv"
  expect_fail validator_run --mode source --gate "$case_dir/route-gate.tsv" \
    --frozen-contract "$frozen" --frozen-contract-sha256 "$frozen_sha" \
    --evidence-root "$case_dir" --revision "$revision"
}

make_route_case wrong-kind source '.kind="compile"'
make_route_case wrong-route source '.path="/substituted"'
make_route_case wrong-revision compile '.revision="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"'
make_route_case wrong-status mount '.status="pending"'
make_route_case extra-key source '.unexpected=true'
make_route_case zero-differential differential '.cases=0'
make_route_case failed-differential differential '.passed=false'
make_route_case self-approval approval '.reviewer_identity="route-author-1"'
make_route_case unbound-approval approval '.bindings.source_sha256="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"'

same_path="$runtime/same-path.tsv"
source_ref=$(awk -F '\t' 'NR == 2 { split($10, entry, ";"); print entry[1] }' "$single_gate")
awk -F '\t' -v source_ref="$source_ref" 'BEGIN { OFS=FS }
  NR == 2 { split($10, entry, ";"); sub(/^source=/, "compile=", source_ref); entry[2]=source_ref; $10=entry[1]; for (i=2;i<=5;i++) $10=$10 ";" entry[i] }
  { print }
' "$single_gate" >"$same_path"
expect_fail validator_run --mode source --gate "$same_path" --frozen-contract "$frozen" \
  --frozen-contract-sha256 "$frozen_sha" --evidence-root "$synthetic" --revision "$revision"

duplicate_bytes_dir="$runtime/duplicate-bytes"
mkdir -p "$duplicate_bytes_dir/evidence"
cp "$single_gate" "$duplicate_bytes_dir/route-gate.tsv"
cp "$synthetic/evidence/route-1-"*.json "$duplicate_bytes_dir/evidence/"
compile_file="$duplicate_bytes_dir/evidence/route-1-compile.json"
old_compile_sha=$(sha256sum "$compile_file" | awk '{print $1}')
cp "$duplicate_bytes_dir/evidence/route-1-source.json" "$compile_file"
new_compile_sha=$(sha256sum "$compile_file" | awk '{print $1}')
sed "s/$old_compile_sha/$new_compile_sha/" "$duplicate_bytes_dir/route-gate.tsv" >"$duplicate_bytes_dir/gate.new"
mv "$duplicate_bytes_dir/gate.new" "$duplicate_bytes_dir/route-gate.tsv"
expect_fail validator_run --mode source --gate "$duplicate_bytes_dir/route-gate.tsv" \
  --frozen-contract "$frozen" --frozen-contract-sha256 "$frozen_sha" \
  --evidence-root "$duplicate_bytes_dir" --revision "$revision"

extra_evidence="$runtime/extra-evidence.tsv"
awk -F '\t' 'BEGIN { OFS=FS } NR == 2 { $10=$10 ";extra=evidence/route-1-source.json@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" } { print }' \
  "$single_gate" >"$extra_evidence"
expect_fail validator_run --mode source --gate "$extra_evidence" --frozen-contract "$frozen" \
  --frozen-contract-sha256 "$frozen_sha" --evidence-root "$synthetic" --revision "$revision"

substituted_gate="$runtime/substituted-route.tsv"
awk -F '\t' 'BEGIN { OFS=FS } NR == 2 { $2="/substituted" } { print }' "$current_gate" >"$substituted_gate"
expect_fail validator_run "${source_args[@]}" --gate "$substituted_gate"
changed_frozen="$runtime/changed-frozen.tsv"
awk -F '\t' 'BEGIN { OFS=FS } NR == 2 { $3="admin" } { print }' "$frozen" >"$changed_frozen"
changed_manifest="$runtime/changed-frozen-assets.sha256"
{
  printf '%s  route-gate.tsv\n' "$(sha256sum "$current_gate" | awk '{print $1}')"
  printf '%s  validate-route-gate\n' "$(sha256sum "$validator" | awk '{print $1}')"
  printf '%s  migration-compatibility.env\n' \
    "$(sha256sum "$repo/packaging/common/lmm-api/migration-compatibility.env" | awk '{print $1}')"
  printf '%s  frozen-route-auth.tsv\n' "$frozen_sha"
} >"$changed_manifest"
changed_manifest_sha=$(sha256sum "$changed_manifest" | awk '{print $1}')
expect_fail env LMM_ROUTE_GATE_TEST_MODE=1 "$validator" --snapshot-dir "$runtime/changed-frozen-snapshot" \
  --mode source --assets-manifest "$changed_manifest" --assets-manifest-sha256 "$changed_manifest_sha" \
  --gate "$current_gate" --frozen-contract "$changed_frozen" --frozen-contract-sha256 "$frozen_sha" \
  --evidence-root "$repo" --revision "$revision"
expect_fail env ROUTE_FROZEN_AUTH_PATH="$changed_frozen" \
  "$repo/apps/api-rust/tests/scripts/check-route-plan.sh"

make_migration_case() {
  local name=$1 file=$2 filter=$3
  local case_dir="$runtime/migration-$name" old_sha new_sha
  mkdir -p "$case_dir"
  cp "$synthetic/"{route-gate.tsv,frozen-route-auth.tsv,validate-route-gate,migration-compatibility.env} "$case_dir/"
  cp -R "$synthetic/evidence" "$case_dir/"
  cp "$synthetic/"{schema-contract.json,n-minus-one.json,migration-approval.json} "$case_dir/"
  old_sha=$(sha256sum "$case_dir/$file" | awk '{print $1}')
  jq "$filter" "$case_dir/$file" >"$case_dir/$file.new"
  mv "$case_dir/$file.new" "$case_dir/$file"
  new_sha=$(sha256sum "$case_dir/$file" | awk '{print $1}')
  sed "s/$old_sha/$new_sha/" "$case_dir/migration-compatibility.env" >"$case_dir/manifest.new"
  mv "$case_dir/manifest.new" "$case_dir/migration-compatibility.env"
  expect_fail validator_run --mode activate --gate "$case_dir/route-gate.tsv" \
    --frozen-contract "$case_dir/frozen-route-auth.tsv" --frozen-contract-sha256 "$synthetic_frozen_sha" \
    --evidence-root "$case_dir" --revision "$revision" --migration-compatibility "$case_dir/migration-compatibility.env"
}
make_migration_case short-duration n-minus-one.json '.duration_seconds=599'
make_migration_case restored-database n-minus-one.json '.database_restored=true'
make_migration_case wrong-n-revision n-minus-one.json '.n_revision="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"'
make_migration_case migration-extra-key schema-contract.json '.unexpected=true'
make_migration_case migration-self-approval migration-approval.json '.reviewer_identity="migration-author"'
make_migration_case migration-unbound-approval migration-approval.json '.bindings.schema_contract_sha256="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"'

swapped_labels="$runtime/swapped-labels.tsv"
awk -F '\t' 'BEGIN { OFS=FS }
  NR == 2 {
    split($10, entry, ";")
    source=entry[1]; compile=entry[2]
    sub(/^source=/, "compile=", source)
    sub(/^compile=/, "source=", compile)
    entry[1]=source; entry[2]=compile
    $10=entry[1]; for (i=2;i<=5;i++) $10=$10 ";" entry[i]
  }
  { print }
' "$single_gate" >"$swapped_labels"
expect_fail validator_run --mode source --gate "$swapped_labels" --frozen-contract "$frozen" \
  --frozen-contract-sha256 "$frozen_sha" --evidence-root "$synthetic" --revision "$revision"

unsafe_file="$runtime/unsafe-file"
mkdir -p "$unsafe_file/evidence"
cp "$single_gate" "$unsafe_file/route-gate.tsv"
cp "$synthetic/evidence/route-1-"*.json "$unsafe_file/evidence/"
chmod 0666 "$unsafe_file/evidence/route-1-source.json"
expect_fail validator_run --mode source --gate "$unsafe_file/route-gate.tsv" \
  --frozen-contract "$frozen" --frozen-contract-sha256 "$frozen_sha" \
  --evidence-root "$unsafe_file" --revision "$revision"

unsafe_parent="$runtime/unsafe-parent"
mkdir -p "$unsafe_parent/evidence"
cp "$single_gate" "$unsafe_parent/route-gate.tsv"
cp "$synthetic/evidence/route-1-"*.json "$unsafe_parent/evidence/"
chmod 0777 "$unsafe_parent/evidence"
expect_fail validator_run --mode source --gate "$unsafe_parent/route-gate.tsv" \
  --frozen-contract "$frozen" --frozen-contract-sha256 "$frozen_sha" \
  --evidence-root "$unsafe_parent" --revision "$revision"

replacement="$runtime/snapshot-replacement"
mkdir -p "$replacement"
cp "$current_gate" "$replacement/route-gate.tsv"
cp "$frozen" "$replacement/frozen-route-auth.tsv"
replacement_snapshot=$(mktemp -d "$runtime/replacement-snapshot.XXXXXXXX")
ready="$runtime/snapshot.ready"
release="$runtime/snapshot.release"
replacement_manifest="$replacement/route-gate-assets.sha256"
{
  printf '%s  route-gate.tsv\n' "$(sha256sum "$replacement/route-gate.tsv" | awk '{print $1}')"
  printf '%s  validate-route-gate\n' "$(sha256sum "$validator" | awk '{print $1}')"
  printf '%s  migration-compatibility.env\n' \
    "$(sha256sum "$repo/packaging/common/lmm-api/migration-compatibility.env" | awk '{print $1}')"
  printf '%s  frozen-route-auth.tsv\n' "$(sha256sum "$replacement/frozen-route-auth.tsv" | awk '{print $1}')"
} >"$replacement_manifest"
replacement_manifest_sha=$(sha256sum "$replacement_manifest" | awk '{print $1}')
LMM_ROUTE_GATE_TEST_MODE=1 LMM_ROUTE_GATE_TEST_SNAPSHOT_READY_FILE="$ready" \
  LMM_ROUTE_GATE_TEST_SNAPSHOT_RELEASE_FILE="$release" \
  "$validator" --snapshot-dir "$replacement_snapshot" --mode source \
  --assets-manifest "$replacement_manifest" --assets-manifest-sha256 "$replacement_manifest_sha" \
  --gate "$replacement/route-gate.tsv" --frozen-contract "$replacement/frozen-route-auth.tsv" \
  --frozen-contract-sha256 "$frozen_sha" --evidence-root "$replacement" --revision "$revision" &
replacement_pid=$!
for _ in {1..1000}; do [[ -e $ready ]] && break; sleep 0.01; done
[[ -e $ready ]] || fail 'validator did not report a completed snapshot'
sed -n '1,2p' "$current_gate" >"$replacement/route-gate.tsv"
: >"$release"
wait "$replacement_pid" || fail 'post-snapshot source replacement changed validation outcome'

manifest_swap="$runtime/manifest-swap"
mkdir -p "$manifest_swap"
cp "$current_gate" "$manifest_swap/route-gate.tsv"
cp "$frozen" "$manifest_swap/frozen-route-auth.tsv"
cp "$repo/packaging/common/lmm-api/migration-compatibility.env" "$manifest_swap/migration-compatibility.env"
cp "$validator" "$manifest_swap/validate-route-gate"
manifest_swap_file="$manifest_swap/route-gate-assets.sha256"
(
  cd "$manifest_swap"
  sha256sum route-gate.tsv validate-route-gate migration-compatibility.env frozen-route-auth.tsv >"$manifest_swap_file"
)
manifest_swap_sha=$(sha256sum "$manifest_swap_file" | awk '{print $1}')
manifest_swap_ready="$runtime/manifest-swap.ready"
manifest_swap_release="$runtime/manifest-swap.release"
manifest_swap_snapshot=$(mktemp -d "$runtime/manifest-swap-snapshot.XXXXXXXX")
LMM_ROUTE_GATE_TEST_MODE=1 \
  LMM_ROUTE_GATE_TEST_BEFORE_MANIFEST_COPY_READY_FILE="$manifest_swap_ready" \
  LMM_ROUTE_GATE_TEST_BEFORE_MANIFEST_COPY_RELEASE_FILE="$manifest_swap_release" \
  "$validator" --snapshot-dir "$manifest_swap_snapshot" --mode source \
  --assets-manifest "$manifest_swap_file" --assets-manifest-sha256 "$manifest_swap_sha" \
  --gate "$manifest_swap/route-gate.tsv" --frozen-contract "$manifest_swap/frozen-route-auth.tsv" \
  --evidence-root "$manifest_swap" --revision "$revision" >"$runtime/manifest-swap.out" 2>"$runtime/manifest-swap.err" &
manifest_swap_pid=$!
for _ in {1..1000}; do [[ -e $manifest_swap_ready ]] && break; sleep 0.01; done
[[ -e $manifest_swap_ready ]] || fail 'validator did not expose its manifest snapshot boundary'
mv "$manifest_swap_file" "$manifest_swap_file.original"
ln -s "$(basename "$manifest_swap_file.original")" "$manifest_swap_file"
: >"$manifest_swap_release"
if wait "$manifest_swap_pid"; then fail 'manifest symlink replacement was accepted'; fi

asset_swap="$runtime/asset-swap"
mkdir -p "$asset_swap"
cp "$current_gate" "$asset_swap/route-gate.tsv"
cp "$frozen" "$asset_swap/frozen-route-auth.tsv"
cp "$repo/packaging/common/lmm-api/migration-compatibility.env" "$asset_swap/migration-compatibility.env"
cp "$validator" "$asset_swap/validate-route-gate"
asset_swap_file="$asset_swap/route-gate-assets.sha256"
(
  cd "$asset_swap"
  sha256sum route-gate.tsv validate-route-gate migration-compatibility.env frozen-route-auth.tsv >"$asset_swap_file"
)
asset_swap_sha=$(sha256sum "$asset_swap_file" | awk '{print $1}')
asset_swap_ready="$runtime/asset-swap.ready"
asset_swap_release="$runtime/asset-swap.release"
asset_swap_snapshot=$(mktemp -d "$runtime/asset-swap-snapshot.XXXXXXXX")
LMM_ROUTE_GATE_TEST_MODE=1 \
  LMM_ROUTE_GATE_TEST_BEFORE_ASSET_COPY_READY_FILE="$asset_swap_ready" \
  LMM_ROUTE_GATE_TEST_BEFORE_ASSET_COPY_RELEASE_FILE="$asset_swap_release" \
  "$validator" --snapshot-dir "$asset_swap_snapshot" --mode source \
  --assets-manifest "$asset_swap_file" --assets-manifest-sha256 "$asset_swap_sha" \
  --gate "$asset_swap/route-gate.tsv" --frozen-contract "$asset_swap/frozen-route-auth.tsv" \
  --evidence-root "$asset_swap" --revision "$revision" >"$runtime/asset-swap.out" 2>"$runtime/asset-swap.err" &
asset_swap_pid=$!
for _ in {1..1000}; do [[ -e $asset_swap_ready ]] && break; sleep 0.01; done
[[ -e $asset_swap_ready ]] || fail 'validator did not expose its asset snapshot boundary'
sed -n '1,2p' "$current_gate" >"$asset_swap/route-gate.tsv"
: >"$asset_swap_release"
if wait "$asset_swap_pid"; then fail 'asset replacement after manifest snapshot was accepted'; fi

printf 'route-gate frozen membership and independent evidence contracts verified\n'
