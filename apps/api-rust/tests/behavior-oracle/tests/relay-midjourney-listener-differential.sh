#!/usr/bin/env bash
# Read-only loopback differential for the Midjourney route boundary.
#
# This gate deliberately sends anonymous malformed requests only.  It proves
# that Go and Rust expose the same paths and fail closed before an upstream or
# durable task write is reached.  Positive provider/accounting scenarios must
# use a separate isolated fixture with seeded channels and are not credited by
# this boundary-only gate.
set -Eeuo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root && $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || {
    echo 'LMM_GO_ORACLE_ROOT must be an absolute external non-symlink Go oracle tree' >&2
    exit 2
}
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in
    "$repo_root"|"$repo_root"/*) echo 'Go oracle must be outside the repository' >&2; exit 2 ;;
esac
rust_binary=${LMM_MJ_RUST_BINARY:-"$repo_root/apps/api-rust/target/debug/lmm-api-rs"}
[[ -x $rust_binary ]] || { echo "Rust binary is unavailable: $rust_binary" >&2; exit 1; }

runtime=$(mktemp -d /tmp/lmm-relay-midjourney-differential.XXXXXX)
pg_pid='' valkey_pid='' go_pid='' rust_pid=''
cleanup() {
    for pid in "$go_pid" "$rust_pid" "$valkey_pid"; do
        if [[ $pid =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ -d $runtime/pg ]]; then
        pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
    fi
    case "$runtime" in
        /tmp/lmm-relay-midjourney-differential.*) rm -rf -- "$runtime" ;;
        *) echo "refusing unexpected cleanup target: $runtime" >&2 ;;
    esac
}
trap cleanup EXIT INT TERM

free_port() {
    local candidate
    while :; do
        candidate=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000))
        if [[ -z $(ss -H -ltn "sport = :$candidate" 2>/dev/null) ]]; then
            echo "$candidate"
            return
        fi
    done
}

for command in createdb createuser curl go initdb jq pg_ctl postgres psql ss valkey-cli valkey-server; do
    command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo 'PostgreSQL 18 is required' >&2; exit 1; }
pg_port=$(free_port)
go_port=$(free_port)
rust_port=$(free_port)
valkey_port=$(free_port)

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
    -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createuser -h 127.0.0.1 -p "$pg_port" lmm_test_mj_diff
createdb -h 127.0.0.1 -p "$pg_port" -O lmm_test_mj_diff lmm_test_mj_diff
psql -h 127.0.0.1 -p "$pg_port" -U lmm_test_mj_diff -d lmm_test_mj_diff \
    -v ON_ERROR_STOP=1 -c 'CREATE SCHEMA lmm_test_mj_diff AUTHORIZATION lmm_test_mj_diff' >/dev/null
sed 's/public\./lmm_test_mj_diff./g' \
    "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" \
    >"$runtime/baseline.sql"
# Expand only the disposable Rust schema after the frozen baseline.
sed "s/__LMM_APP_SCHEMA__/lmm_test_mj_diff/g" \
  "$repo_root/apps/api-rust/migrations/0018_subscription_amount_snapshots.sql" \
  >>"$runtime/baseline.sql"
PGOPTIONS='-c search_path=lmm_test_mj_diff' \
    psql -h 127.0.0.1 -p "$pg_port" -U lmm_test_mj_diff -d lmm_test_mj_diff \
    -v ON_ERROR_STOP=1 -f "$runtime/baseline.sql" >/dev/null
sed 's/__LMM_APP_SCHEMA__/lmm_test_mj_diff/g' \
    "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
    | PGOPTIONS='-c search_path=lmm_test_mj_diff' \
      psql -h 127.0.0.1 -p "$pg_port" -U lmm_test_mj_diff -d lmm_test_mj_diff \
      -v ON_ERROR_STOP=1 >/dev/null
PGOPTIONS='-c search_path=lmm_test_mj_diff' \
    psql -h 127.0.0.1 -p "$pg_port" -U lmm_test_mj_diff -d lmm_test_mj_diff \
    -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE TABLE lmm_schema_contract (
    singleton BOOLEAN PRIMARY KEY,
    min_reader_version BIGINT NOT NULL,
    max_reader_version BIGINT NOT NULL
);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
SQL

valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no \
    --dir "$runtime" --logfile "$runtime/valkey.log" >/dev/null 2>&1 &
valkey_pid=$!
for _ in {1..200}; do
    valkey-cli -h 127.0.0.1 -p "$valkey_port" ping >/dev/null 2>&1 && break
    sleep .05
done

mkdir -p "$runtime/go-source/web/dist"
cp -a "$legacy_root/." "$runtime/go-source/"
: >"$runtime/go-source/web/dist/index.html"
(cd "$runtime/go-source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .)
dsn="postgresql://lmm_test_mj_diff@127.0.0.1:$pg_port/lmm_test_mj_diff?options=-csearch_path%3Dlmm_test_mj_diff"

LMM_LOCAL_ACCEPTANCE=true LMM_API_BIND_ADDRESS=127.0.0.1 \
    PGOPTIONS='-c search_path=lmm_test_mj_diff' SQL_DSN="$dsn" PORT="$go_port" \
    REDIS_CONN_STRING="redis://127.0.0.1:$valkey_port/5" \
    SESSION_SECRET='MidjourneyDifferentialSession-2026' \
    CRYPTO_SECRET='MidjourneyDifferentialCrypto-2026' PASSWORD_LOGIN_ENABLED=true \
    GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
    MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release \
    "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
go_pid=$!
for _ in {1..6000}; do
    if ! kill -0 "$go_pid" 2>/dev/null; then
        break
    fi
    [[ $(curl --silent --output /dev/null --write-out '%{http_code}' \
        "http://127.0.0.1:$go_port/api/status" || true) == 200 ]] && break
    sleep .05
done
[[ $(curl --silent --output /dev/null --write-out '%{http_code}' \
    "http://127.0.0.1:$go_port/api/status") == 200 ]] || {
    sed -n '1,200p' "$runtime/go.log" >&2
    exit 1
}

LMM_RS_TEST_INSTANCE=1 LMM_RS_TEST_VALKEY_PORT="$valkey_port" \
    LMM_RS_SLOT=single LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" \
    PGOPTIONS='-c search_path=lmm_test_mj_diff' DATABASE_URL="$dsn" \
    VALKEY_URL="redis://127.0.0.1:$valkey_port/6" LMM_SCHEMA_CONTRACT=1 \
    SESSION_SECRET='MidjourneyDifferentialSession-2026' \
    CRYPTO_SECRET='MidjourneyDifferentialCrypto-2026' PASSWORD_LOGIN_ENABLED=true \
    GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
    MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none VERSION=v0.0.0 \
    "$rust_binary" >"$runtime/rust.log" 2>&1 &
rust_pid=$!
for _ in {1..6000}; do
    if ! kill -0 "$rust_pid" 2>/dev/null; then
        break
    fi
    [[ $(curl --silent --output /dev/null --write-out '%{http_code}' \
        "http://127.0.0.1:$rust_port/readyz" || true) == 200 ]] && break
    sleep .05
done
[[ $(curl --silent --output /dev/null --write-out '%{http_code}' \
    "http://127.0.0.1:$rust_port/readyz") == 200 ]] || {
    sed -n '1,240p' "$runtime/rust.log" >&2
    exit 1
}

compare_case() {
    local name=$1 method=$2 path=$3 body=${4:-}
    local go_body="$runtime/go-$name.body" rust_body="$runtime/rust-$name.body"
    local go_status="$runtime/go-$name.status" rust_status="$runtime/rust-$name.status"
    if [[ $method == GET ]]; then
        curl --silent --show-error --max-time 20 -o "$go_body" -w '%{http_code}' \
            "http://127.0.0.1:$go_port$path" >"$go_status"
        curl --silent --show-error --max-time 20 -o "$rust_body" -w '%{http_code}' \
            "http://127.0.0.1:$rust_port$path" >"$rust_status"
    else
        curl --silent --show-error --max-time 20 -H 'content-type: application/json' \
            -X "$method" --data-binary "$body" -o "$go_body" -w '%{http_code}' \
            "http://127.0.0.1:$go_port$path" >"$go_status"
        curl --silent --show-error --max-time 20 -H 'content-type: application/json' \
            -X "$method" --data-binary "$body" -o "$rust_body" -w '%{http_code}' \
            "http://127.0.0.1:$rust_port$path" >"$rust_status"
    fi
    printf '%s\t' "$method $path"
    if [[ $(<"$go_status") != $(<"$rust_status") ]]; then
        echo "status-mismatch go=$(<"$go_status") rust=$(<"$rust_status")"
        jq -S . "$go_body" 2>/dev/null || sed -n '1,120p' "$go_body"
        jq -S . "$rust_body" 2>/dev/null || sed -n '1,120p' "$rust_body"
        return 1
    fi
    # Go appends a freshly generated request id to every auth failure while
    # the in-process Rust route is normally wrapped by the listener boundary.
    # Compare the stable wire envelope and retain the fact that both sides
    # generated an auth failure; the id itself is intentionally non-deterministic.
    normalize_body() {
        jq -S 'if .error?.message? then .error.message |= sub("\\(request id: [^)]*\\)$"; "(request id: <request-id>)") else . end' "$1"
    }
    if ! diff -q <(normalize_body "$go_body") <(normalize_body "$rust_body") >/dev/null; then
        echo 'body-mismatch'
        echo 'Go:'; normalize_body "$go_body" 2>/dev/null || sed -n '1,120p' "$go_body"
        echo 'Rust:'; normalize_body "$rust_body" 2>/dev/null || sed -n '1,120p' "$rust_body"
        return 1
    fi
    echo "ok status=$(<"$go_status")"
}

failed=0
index=0
while IFS=$'\t' read -r method path body; do
    [[ -n $method ]] || continue
    index=$((index + 1))
    compare_case "$index" "$method" "$path" "$body" || failed=1
done <<'CASES'
GET	/proxy/mj/image/not-present	
POST	/proxy/mj/insight-face/swap	{}
POST	/proxy/mj/submit/action	{}
POST	/proxy/mj/submit/blend	{}
POST	/proxy/mj/submit/change	{}
POST	/proxy/mj/submit/describe	{}
POST	/proxy/mj/submit/edits	{}
POST	/proxy/mj/submit/imagine	{}
POST	/proxy/mj/submit/modal	{}
POST	/proxy/mj/submit/shorten	{}
POST	/proxy/mj/submit/simple-change	{}
POST	/proxy/mj/submit/upload-discord-images	{}
POST	/proxy/mj/submit/video	{}
GET	/proxy/mj/task/not-present/fetch	
GET	/proxy/mj/task/not-present/image-seed	
POST	/proxy/mj/task/list-by-condition	{}
GET	/mj/image/not-present	
POST	/mj/insight-face/swap	{}
POST	/mj/submit/action	{}
POST	/mj/submit/blend	{}
POST	/mj/submit/change	{}
POST	/mj/submit/describe	{}
POST	/mj/submit/edits	{}
POST	/mj/submit/imagine	{}
POST	/mj/submit/modal	{}
POST	/mj/submit/shorten	{}
POST	/mj/submit/simple-change	{}
POST	/mj/submit/upload-discord-images	{}
POST	/mj/submit/video	{}
GET	/mj/task/not-present/fetch	
GET	/mj/task/not-present/image-seed	
POST	/mj/task/list-by-condition	{}
CASES

jq -cn --argjson routes "$index" --argjson failed "$failed" \
    '{test:"relay-midjourney-listener-differential",mode:"anonymous-boundary",routes:$routes,failed:$failed,approval_credit:false,result:(if $failed==0 then "passed" else "failed" end)}'
exit "$failed"
