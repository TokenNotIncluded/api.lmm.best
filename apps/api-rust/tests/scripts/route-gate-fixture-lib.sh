#!/usr/bin/env bash

route_gate_fixture_digest() {
  printf '%064x' "$1"
}

route_gate_fixture_write_route() {
  local root=$1 index=$2 method=$3 path=$4
  local prefix="evidence/route-$index" source_file compile_file mount_file differential_file approval_file
  local implementation artifact command router transcript source_sha compile_sha mount_sha differential_sha approval_sha
  [[ $method != *[\"\\]* && $path != *[\"\\]* ]] || return 1
  source_file="$prefix-source.json"
  compile_file="$prefix-compile.json"
  mount_file="$prefix-mount.json"
  differential_file="$prefix-differential.json"
  approval_file="$prefix-approval.json"
  implementation=$(route_gate_fixture_digest "$((index * 20 + 1))")
  artifact=$(route_gate_fixture_digest "$((index * 20 + 2))")
  command=$(route_gate_fixture_digest "$((index * 20 + 3))")
  router=$(route_gate_fixture_digest "$((index * 20 + 4))")
  transcript=$(route_gate_fixture_digest "$((index * 20 + 5))")
  printf '{"schema_version":1,"kind":"source","method":"%s","path":"%s","revision":"%s","status":"verified","author_identity":"route-author-%d","implementation_sha256":"%s"}\n' \
    "$method" "$path" "$ROUTE_GATE_FIXTURE_REVISION" "$index" "$implementation" >"$root/$source_file"
  printf '{"schema_version":1,"kind":"compile","method":"%s","path":"%s","revision":"%s","status":"passed","compiler":"rustc-fixture","artifact_sha256":"%s","command_sha256":"%s"}\n' \
    "$method" "$path" "$ROUTE_GATE_FIXTURE_REVISION" "$artifact" "$command" >"$root/$compile_file"
  printf '{"schema_version":1,"kind":"mount","method":"%s","path":"%s","revision":"%s","status":"verified","listener":"production-http","route_count":1,"router_path":"%s","router_sha256":"%s"}\n' \
    "$method" "$path" "$ROUTE_GATE_FIXTURE_REVISION" "${ROUTE_GATE_FIXTURE_ROUTER_PATH:-synthetic/router.rs}" "$router" >"$root/$mount_file"
  printf '{"schema_version":1,"kind":"differential","method":"%s","path":"%s","revision":"%s","status":"passed","passed":true,"cases":3,"transcript_sha256":"%s"}\n' \
    "$method" "$path" "$ROUTE_GATE_FIXTURE_REVISION" "$transcript" >"$root/$differential_file"
  source_sha=$(sha256sum "$root/$source_file" | awk '{print $1}')
  compile_sha=$(sha256sum "$root/$compile_file" | awk '{print $1}')
  mount_sha=$(sha256sum "$root/$mount_file" | awk '{print $1}')
  differential_sha=$(sha256sum "$root/$differential_file" | awk '{print $1}')
  printf '{"schema_version":1,"kind":"approval","method":"%s","path":"%s","revision":"%s","status":"approved","decision":"approved","independent":true,"reviewer_identity":"route-reviewer-%d","bindings":{"source_sha256":"%s","compile_sha256":"%s","mount_sha256":"%s","differential_sha256":"%s"}}\n' \
    "$method" "$path" "$ROUTE_GATE_FIXTURE_REVISION" "$index" "$source_sha" "$compile_sha" "$mount_sha" "$differential_sha" \
    >"$root/$approval_file"
  approval_sha=$(sha256sum "$root/$approval_file" | awk '{print $1}')
  ROUTE_GATE_FIXTURE_EVIDENCE="source=$source_file@sha256:$source_sha;compile=$compile_file@sha256:$compile_sha;mount=$mount_file@sha256:$mount_sha;differential=$differential_file@sha256:$differential_sha;approval=$approval_file@sha256:$approval_sha"
}

route_gate_fixture_create() {
  local repo=$1 root=$2 revision=$3 current_gate method path rest index=0
  current_gate="$repo/apps/api-rust/tests/fixtures/routes/route-gate.tsv"
  ROUTE_GATE_FIXTURE_REVISION=$revision
  mkdir -p "$root/evidence"
  IFS= read -r header <"$current_gate"
  printf '%s\n' "$header" >"$root/route-gate.tsv"
  while IFS=$'\t' read -r method path rest; do
    ((index += 1))
    route_gate_fixture_write_route "$root" "$index" "$method" "$path"
    printf '%s\t%s\tpresent\tverified\tmounted\tverified\tapproved\trs\tverified-approved\t%s\n' \
      "$method" "$path" "$ROUTE_GATE_FIXTURE_EVIDENCE" >>"$root/route-gate.tsv"
  done < <(tail -n +2 "$current_gate")

  install -Dm0755 "$repo/packaging/common/lmm-api/validate-route-gate" "$root/validate-route-gate"
  install -Dm0644 "$repo/apps/api-rust/tests/fixtures/routes/frozen-route-auth.tsv" "$root/frozen-route-auth.tsv"
  schema_artifact=$(route_gate_fixture_digest 9001)
  schema_digest=$(route_gate_fixture_digest 9002)
  n_minus_one_artifact=$(route_gate_fixture_digest 9003)
  printf '{"schema_version":1,"kind":"postgres-schema-contract","release_revision":"%s","status":"passed","author_identity":"migration-author","release_artifact_sha256":"%s","schema_sha256":"%s"}\n' \
    "$revision" "$schema_artifact" "$schema_digest" >"$root/schema-contract.json"
  printf '{"schema_version":1,"kind":"postgres-n-minus-one","release_revision":"%s","status":"passed","passed":true,"duration_seconds":600,"database_restored":false,"executor_identity":"migration-executor","n_revision":"%s","n_minus_one_revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","n_artifact_sha256":"%s","n_minus_one_artifact_sha256":"%s"}\n' \
    "$revision" "$revision" "$schema_artifact" "$n_minus_one_artifact" >"$root/n-minus-one.json"
  schema_sha=$(sha256sum "$root/schema-contract.json" | awk '{print $1}')
  n1_sha=$(sha256sum "$root/n-minus-one.json" | awk '{print $1}')
  printf '{"schema_version":1,"kind":"postgres-migration-approval","release_revision":"%s","status":"approved","decision":"approved","independent":true,"reviewer_identity":"migration-reviewer","bindings":{"schema_contract_sha256":"%s","n_minus_one_sha256":"%s"}}\n' \
    "$revision" "$schema_sha" "$n1_sha" >"$root/migration-approval.json"
  approval_sha=$(sha256sum "$root/migration-approval.json" | awk '{print $1}')
  printf 'format=1\nmigration_state=verified-approved\nrelease_revision=%s\nschema_contract=schema-contract.json@sha256:%s\nn_minus_one=n-minus-one.json@sha256:%s\napproval=migration-approval.json@sha256:%s\n' \
    "$revision" "$schema_sha" "$n1_sha" "$approval_sha" >"$root/migration-compatibility.env"
  (
    cd "$root" || return
    sha256sum route-gate.tsv validate-route-gate migration-compatibility.env frozen-route-auth.tsv \
      >route-gate-assets.sha256
  )
}
