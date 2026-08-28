#!/usr/bin/env bash
# Real loopback Go/Rust differential for the PostgreSQL-backed OpenAI relay
# vertical. Both listeners own independent schemas, Valkey instances, and
# provider hit logs; no production endpoint or credential is accepted.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'Go oracle must be external to the repository' >&2; exit 2 ;; esac

runtime_base=${LMM_RELAY_OPENAI_RUNTIME_BASE:-/home/lightjunction/.cache}
runtime=$(mktemp -d "$runtime_base/lmm-relay-openai-differential.XXXXXX")
result_dir=${LMM_RELAY_OPENAI_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_RELAY_OPENAI_RESULT_DIR must be an absolute path without ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
pg_port=${LMM_RELAY_OPENAI_PG_PORT:-45461}
go_port=${LMM_RELAY_OPENAI_GO_PORT:-18461}
rust_port=${LMM_RELAY_OPENAI_RUST_PORT:-38461}
go_valkey_port=${LMM_RELAY_OPENAI_GO_VALKEY_PORT:-16461}
rust_valkey_port=${LMM_RELAY_OPENAI_RUST_VALKEY_PORT:-16462}
provider_port=${LMM_RELAY_OPENAI_PROVIDER_PORT:-48461}
database=lmm_relay_openai
go_schema=lmm_relay_openai_go
rust_schema=lmm_relay_openai_rust
go_role=lmm_relay_openai_go
rust_role=lmm_relay_openai_rust
# shellcheck disable=SC2034 # Values are read indirectly by record_pid/owned_live.
go_pid='' rust_pid='' go_valkey_pid='' rust_valkey_pid='' provider_pid=''
# shellcheck disable=SC2034 # Values are read indirectly through ${!start_name}.
go_start='' rust_start='' go_valkey_start='' rust_valkey_start='' provider_start=''

pid_start_time() { [[ -r /proc/$1/stat ]] || return 1; awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local name=$1 pid=$2 start; printf -v "$name" '%s' "$pid"; start=$(pid_start_time "$pid") || return 1; printf -v "${name}_start" '%s' "$start"; }
owned_live() { local name=$1 pid start_name expected; pid=${!name:-}; start_name="${name}_start"; expected=${!start_name:-}; [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]; }
stop_owned() { local name=$1 pid=${!1:-}; if [[ -n $pid ]] && owned_live "$name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi; printf -v "$name" ''; printf -v "${name}_start" ''; }
cleanup() {
  stop_owned go_pid || true; stop_owned rust_pid || true; stop_owned go_valkey_pid || true; stop_owned rust_valkey_pid || true; stop_owned provider_pid || true
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  if [[ ${LMM_RELAY_OPENAI_KEEP_RUNTIME:-0} == 1 ]]; then echo "retaining runtime: $runtime" >&2; return; fi
  case "$runtime" in "$runtime_base"/lmm-relay-openai-differential.*) find "$runtime" -depth -delete ;; *) echo 'refusing unexpected runtime cleanup target' >&2 ;; esac
}
trap cleanup EXIT INT TERM

for command in awk cargo createdb createuser curl ffmpeg go initdb jq pg_ctl postgres psql python3 ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo 'requires PostgreSQL 18' >&2; exit 1; }
for port in "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port" "$provider_port"; do
  [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] || { echo "occupied port: $port" >&2; exit 2; }
done

rust_target=${LMM_RELAY_OPENAI_CARGO_TARGET_DIR:-"$runtime/cargo-target"}
rust_binary=${LMM_RELAY_OPENAI_RUST_BINARY:-"$rust_target/debug/lmm-api-rs"}
if [[ ${LMM_RELAY_OPENAI_SKIP_RUST_BUILD:-0} != 1 ]]; then
  CARGO_TARGET_DIR="$rust_target" CARGO_BUILD_JOBS=2 cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
fi
[[ -x $rust_binary ]] || { echo "missing Rust binary: $rust_binary" >&2; exit 1; }

mkdir -p "$runtime/go-source/web/dist"
cp -a "$legacy_root/." "$runtime/go-source/"
: >"$runtime/go-source/web/dist/index.html"
(cd "$runtime/go-source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .)

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-c fsync=off -c synchronous_commit=off -c full_page_writes=off -h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$database"
psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE ROLE $go_role LOGIN;
CREATE ROLE $rust_role LOGIN;
CREATE SCHEMA $go_schema AUTHORIZATION $go_role;
CREATE SCHEMA $rust_schema AUTHORIZATION $rust_role;
ALTER ROLE $go_role IN DATABASE $database SET search_path TO $go_schema;
ALTER ROLE $rust_role IN DATABASE $database SET search_path TO $rust_schema;
SQL
schema=$rust_schema
sed "s/public\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/$schema.sql"
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$database" -q -v ON_ERROR_STOP=1 -f "$runtime/$schema.sql" >/dev/null
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$database" -v ON_ERROR_STOP=1 -c "CREATE TABLE $schema.lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL); INSERT INTO $schema.lmm_schema_contract VALUES (TRUE,1,1);" >/dev/null
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE TABLE IF NOT EXISTS open_source_bounty_projects (
  id BIGSERIAL PRIMARY KEY, owner_user_id BIGINT NOT NULL, repository_url TEXT NOT NULL,
  title TEXT NOT NULL, description TEXT NOT NULL, rules TEXT NOT NULL,
  reward_quota BIGINT NOT NULL DEFAULT 0, net_reward_quota BIGINT NOT NULL DEFAULT 0,
  reward_slots BIGINT NOT NULL DEFAULT 0, escrow_quota BIGINT NOT NULL DEFAULT 0,
  platform_fee_rate_bps BIGINT NOT NULL DEFAULT 0, platform_fee_quota BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL, created_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL DEFAULT 0,
  published_at BIGINT NOT NULL DEFAULT 0, closed_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS open_source_bounty_challenges (
  id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL, participant_user_id BIGINT NOT NULL,
  github_handle TEXT NOT NULL, status TEXT NOT NULL, issue_url TEXT NOT NULL DEFAULT '',
  pull_request_url TEXT NOT NULL DEFAULT '', submission_note TEXT NOT NULL DEFAULT '',
  review_note TEXT NOT NULL DEFAULT '', reward_quota BIGINT NOT NULL DEFAULT 0,
  tip_quota BIGINT NOT NULL DEFAULT 0, owner_rating_score BIGINT NOT NULL DEFAULT 0,
  owner_rating_comment TEXT NOT NULL DEFAULT '', owner_rated_at BIGINT NOT NULL DEFAULT 0,
  contributor_rating_score BIGINT NOT NULL DEFAULT 0, contributor_rating_comment TEXT NOT NULL DEFAULT '',
  contributor_rated_at BIGINT NOT NULL DEFAULT 0, owner_rating_overturned BOOLEAN NOT NULL DEFAULT FALSE,
  accepted_at BIGINT NOT NULL DEFAULT 0, submitted_at BIGINT NOT NULL DEFAULT 0,
  reviewed_at BIGINT NOT NULL DEFAULT 0, rejected_at BIGINT NOT NULL DEFAULT 0,
  paid_at BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS open_source_bounty_ledgers (
  id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL, challenge_id BIGINT,
  user_id BIGINT NOT NULL, counterparty_user_id BIGINT, kind TEXT NOT NULL,
  quota BIGINT NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '',
  recipient_read_at BIGINT NOT NULL DEFAULT 0, thanked_at BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS open_source_bounty_disputes (
  id BIGSERIAL PRIMARY KEY, challenge_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
  opened_by_user_id BIGINT NOT NULL, against_user_id BIGINT NOT NULL, reason TEXT NOT NULL,
  statement TEXT NOT NULL, project_title_snapshot TEXT NOT NULL DEFAULT '',
  repository_url_snapshot TEXT NOT NULL DEFAULT '', project_rules_snapshot TEXT NOT NULL DEFAULT '',
  project_escrow_quota_snapshot BIGINT NOT NULL DEFAULT 0, challenge_status_snapshot TEXT NOT NULL DEFAULT '',
  issue_url_snapshot TEXT NOT NULL DEFAULT '', pull_request_url_snapshot TEXT NOT NULL DEFAULT '',
  submission_note_snapshot TEXT NOT NULL DEFAULT '', review_note_snapshot TEXT NOT NULL DEFAULT '',
  reward_quota_snapshot BIGINT NOT NULL DEFAULT 0, tip_quota_snapshot BIGINT NOT NULL DEFAULT 0,
  owner_rating_score_snapshot BIGINT NOT NULL DEFAULT 0, owner_rating_comment_snapshot TEXT NOT NULL DEFAULT '',
  contributor_rating_score_snapshot BIGINT NOT NULL DEFAULT 0, contributor_rating_comment_snapshot TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, resolution TEXT NOT NULL DEFAULT '', resolved_by_user_id BIGINT,
  created_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL DEFAULT 0, resolved_at BIGINT NOT NULL DEFAULT 0
);
SQL

go_valkey_secret="GoRelayOpenAI-$(openssl rand -hex 24)!"
rust_valkey_secret="RustRelayOpenAI-$(openssl rand -hex 24)!"
valkey-server --bind 127.0.0.1 --port "$go_valkey_port" --save '' --appendonly no --requirepass "$go_valkey_secret" --dir "$runtime" --logfile "$runtime/go-valkey.log" >/dev/null 2>&1 & record_pid go_valkey_pid "$!"
valkey-server --bind 127.0.0.1 --port "$rust_valkey_port" --save '' --appendonly no --requirepass "$rust_valkey_secret" --dir "$runtime" --logfile "$runtime/rust-valkey.log" >/dev/null 2>&1 & record_pid rust_valkey_pid "$!"
for _ in {1..200}; do VALKEYCLI_AUTH="$go_valkey_secret" valkey-cli -h 127.0.0.1 -p "$go_valkey_port" ping >/dev/null 2>&1 && break; sleep .05; done
for _ in {1..200}; do VALKEYCLI_AUTH="$rust_valkey_secret" valkey-cli -h 127.0.0.1 -p "$rust_valkey_port" ping >/dev/null 2>&1 && break; sleep .05; done

go_dsn="postgresql://$go_role@127.0.0.1:$pg_port/$database?sslmode=disable&options=-csearch_path%3D$go_schema"
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$database?options=-csearch_path%3D$rust_schema"

seed() {
  local role=$1 schema=$2 provider=$3
  PGPASSWORD='' PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "$role" -d "$database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
INSERT INTO setups (id,version,initialized_at) VALUES (1,'relay-openai',1) ON CONFLICT (id) DO NOTHING;
INSERT INTO users (id,username,password,role,status,email,quota,used_quota,request_count,"group",setting,created_at,last_login_at,auth_version)
VALUES (42,'relay-user','unused',1,1,'relay@example.test',1000000,0,0,'default','{}',1,0,1)
ON CONFLICT (id) DO UPDATE SET quota=1000000,status=1,"group"='default';
INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,"group",cross_group_retry)
VALUES (73,42,'relayprobe',1,'relay-token',1,1,-1,1000000,FALSE,FALSE,'','',0,'default',FALSE)
ON CONFLICT (id) DO UPDATE SET status=1,remain_quota=1000000,"group"='default';
INSERT INTO channels (id,type,key,status,name,weight,created_time,base_url,"group",used_quota,models,model_mapping,priority,auto_ban,param_override,header_override)
VALUES (7,1,'provider-owned-secret',1,'loopback',10,1,'http://127.0.0.1:$provider','default',0,'gpt-test,gpt-test-openai-compact','',10,0,'','')
ON CONFLICT (id) DO UPDATE SET status=1,base_url=EXCLUDED.base_url,key=EXCLUDED.key;
INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-test',7,TRUE,10,10)
ON CONFLICT ("group",model,channel_id) DO UPDATE SET enabled=TRUE;
INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-test-openai-compact',7,TRUE,10,10)
ON CONFLICT ("group",model,channel_id) DO UPDATE SET enabled=TRUE;
INSERT INTO options (key,value) VALUES
 ('ModelPrice','{"gpt-test":0.000002,"*-openai-compact":0.000002}'),('QuotaPerUnit','500000'),('UserUsableGroups','{"default":"default"}'),('GroupRatio','{"default":1}'),('GroupGroupRatio','{}'),('performance_setting','{"monitor_enabled":false}'),('ModelRequestRateLimitEnabled','false')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value;
SQL
}

PGOPTIONS="-c search_path=$go_schema" SQL_DSN="$go_dsn" PORT="$go_port" REDIS_CONN_STRING="redis://:$go_valkey_secret@127.0.0.1:$go_valkey_port/5" SESSION_SECRET='GoRelayOpenAI-Session-2026-0123456789' CRYPTO_SECRET='GoRelayOpenAI-Crypto-2026-0123456789' PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release "$runtime/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
for _ in {1..6000}; do kill -0 "$go_pid" 2>/dev/null && [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$go_port/api/status" || true) == 200 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$go_port/api/status") == 200 ]] || { sed -n '1,220p' "$runtime/go.log" >&2; exit 1; }

stop_owned go_pid
seed "$go_role" "$go_schema" "$provider_port"
seed "$rust_role" "$rust_schema" "$provider_port"

hits="$runtime/provider-hits.jsonl"
: >"$hits"
python3 -u "$repo_root/apps/api-rust/tests/behavior-oracle/fixtures/relay_openai_provider.py" "$provider_port" "$hits" >"$runtime/provider.log" 2>&1 & record_pid provider_pid "$!"
provider_ready=0
for _ in {1..200}; do
  if curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$provider_port/health" 2>/dev/null | grep -qx 204; then
    provider_ready=1
    break
  fi
  owned_live provider_pid || break
  sleep .05
done
((provider_ready == 1)) || { cat "$runtime/provider.log" >&2; exit 1; }

PGOPTIONS="-c search_path=$go_schema" SQL_DSN="$go_dsn" PORT="$go_port" REDIS_CONN_STRING="redis://:$go_valkey_secret@127.0.0.1:$go_valkey_port/5" SESSION_SECRET='GoRelayOpenAI-Session-2026-0123456789' CRYPTO_SECRET='GoRelayOpenAI-Crypto-2026-0123456789' PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release "$runtime/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
for _ in {1..6000}; do kill -0 "$go_pid" 2>/dev/null && [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$go_port/api/status" || true) == 200 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$go_port/api/status") == 200 ]] || { sed -n '1,220p' "$runtime/go.log" >&2; exit 1; }

PGOPTIONS="-c search_path=$rust_schema" DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$rust_valkey_secret@127.0.0.1:$rust_valkey_port/6" LMM_SCHEMA_CONTRACT=1 LMM_RS_SLOT=blue LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" SESSION_SECRET='RustRelayOpenAI-Session-2026-0123456789' CRYPTO_SECRET='RustRelayOpenAI-Crypto-2026-0123456789' PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none VERSION=v0.0.0 "$rust_binary" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"
for _ in {1..6000}; do kill -0 "$rust_pid" 2>/dev/null && [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz" || true) == 200 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]] || { sed -n '1,220p' "$runtime/rust.log" >&2; exit 1; }

call() {
  local engine=$1 name=$2 path=$3 body=$4 token=${5:-} port prefix
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  prefix="$runtime/$engine.$name"
  curl -sS -D "$prefix.headers" -o "$prefix.body" -w '%{http_code}' -X POST -H 'content-type: application/json' ${token:+-H "authorization: Bearer $token"} --data-binary "$body" "http://127.0.0.1:$port$path" >"$prefix.status"
  jq -e . "$prefix.body" >/dev/null
}

call_multipart() {
  local engine=$1 name=$2 path=$3 token=${4:-} port prefix
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  prefix="$runtime/$engine.$name"
  curl -sS -D "$prefix.headers" -o "$prefix.body" -w '%{http_code}' -X POST \
    ${token:+-H "authorization: Bearer $token"} \
    -F 'model=gpt-test' -F "file=@$runtime/fixture.wav;filename=fixture.wav" \
    "http://127.0.0.1:$port$path" >"$prefix.status"
  jq -e . "$prefix.body" >/dev/null
}

normalize_body() {
  jq 'if .error.message? then .error.message |= sub("\\(request id: [^)]*\\)$"; "(request id: <request-id>)") else . end' "$1"
}

declare -a routes=(
  'chat|/v1/chat/completions|{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}'
  'completion|/v1/completions|{"model":"gpt-test","prompt":"hello"}'
  'responses|/v1/responses|{"model":"gpt-test","input":"hello"}'
  'compact|/v1/responses/compact|{"model":"gpt-test","input":"hello"}'
  'audio-speech|/v1/audio/speech|{"model":"gpt-test","input":"hello","voice":"alloy"}'
  'audio-transcriptions|/v1/audio/transcriptions|{"model":"gpt-test","file":"fixture-audio"}'
  'audio-translations|/v1/audio/translations|{"model":"gpt-test","file":"fixture-audio"}'
  'image-generations|/v1/images/generations|{"model":"gpt-test","prompt":"hello"}'
  'image-edits|/v1/images/edits|{"model":"gpt-test","image":"fixture-image"}'
)
cases=0
for route in "${routes[@]}"; do
  IFS='|' read -r name path body <<<"$route"
  call go "$name-anon" "$path" "$body"
  call rust "$name-anon" "$path" "$body"
  diff -u "$runtime/go.$name-anon.status" "$runtime/rust.$name-anon.status"
  diff -u <(normalize_body "$runtime/go.$name-anon.body" | jq -S .) <(normalize_body "$runtime/rust.$name-anon.body" | jq -S .)
  call go "$name-ok" "$path" "$body" sk-relayprobe
  call rust "$name-ok" "$path" "$body" sk-relayprobe
  diff -u "$runtime/go.$name-ok.status" "$runtime/rust.$name-ok.status"
  diff -u <(normalize_body "$runtime/go.$name-ok.body" | jq -S .) <(normalize_body "$runtime/rust.$name-ok.body" | jq -S .)
  cases=$((cases + 2))
done

jq -s -e '
  length == 14
  and all(.[]; .authorization == "Bearer provider-owned-secret" and .body.model == "gpt-test")
  and (group_by(.path) | all(length == 2 and .[0].body == .[1].body and .[0].content_type == .[1].content_type))
' "$hits" >/dev/null

# Exercise the valid multipart branch as well as the malformed JSON boundary
# above. A tiny PCM WAV is sufficient for Go's duration counter and keeps the
# upload fixture deterministic and local.
ffmpeg -hide_banner -loglevel error -f lavfi -i anullsrc=r=8000:cl=mono -t 0.1 \
  -c:a pcm_s16le "$runtime/fixture.wav" -y
for route in 'audio-transcriptions|/v1/audio/transcriptions' 'audio-translations|/v1/audio/translations'; do
  IFS='|' read -r name path <<<"$route"
  call_multipart go "$name-valid-anon" "$path"
  call_multipart rust "$name-valid-anon" "$path"
  diff -u "$runtime/go.$name-valid-anon.status" "$runtime/rust.$name-valid-anon.status"
  diff -u <(normalize_body "$runtime/go.$name-valid-anon.body" | jq -S .) \
    <(normalize_body "$runtime/rust.$name-valid-anon.body" | jq -S .)
  call_multipart go "$name-valid-ok" "$path" sk-relayprobe
  call_multipart rust "$name-valid-ok" "$path" sk-relayprobe
  diff -u "$runtime/go.$name-valid-ok.status" "$runtime/rust.$name-valid-ok.status"
  diff -u <(normalize_body "$runtime/go.$name-valid-ok.body" | jq -S .) \
    <(normalize_body "$runtime/rust.$name-valid-ok.body" | jq -S .)
  cases=$((cases + 2))
done
jq -s -e '
  length == 18
  and all(.[]; .authorization == "Bearer provider-owned-secret" and .body.model == "gpt-test")
  and (group_by(.path) | all(length == 2 and .[0].body == .[1].body and .[0].content_type == .[1].content_type))
' "$hits" >/dev/null
for engine in go rust; do
  psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$database" -At -v ON_ERROR_STOP=1 -c "SELECT 1" >/dev/null
done

if [[ -n $result_dir ]]; then
  openai_route_index=0
  media_route_index=0
  for route in "${routes[@]}"; do
    IFS='|' read -r name path body <<<"$route"
    if [[ $path == /v1/audio/* || $path == /v1/images/* ]]; then
      media_route_index=$((media_route_index + 1))
      output_name="relay-media-$media_route_index"
      scope="relay-media"
    else
      openai_route_index=$((openai_route_index + 1))
      output_name="relay-openai-$openai_route_index"
      scope="relay-openai"
    fi
    provider_hits=2
    route_cases=2
    if [[ $path == /v1/audio/transcriptions || $path == /v1/audio/translations ]]; then
      route_cases=4
      provider_hits=2
    fi
    jq -cn --arg method POST --arg path "$path" --arg route "$name" --argjson provider_hits "$provider_hits" --argjson route_cases "$route_cases" \
      --arg scope "$scope" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:$scope,cases:$route_cases,route_fixture:$route,provider_hits:$provider_hits,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$result_dir/$output_name.json"
  done
fi

jq -cn --argjson cases "$cases" --arg provider "127.0.0.1:$provider_port" \
  '{test:"relay-openai-listener-differential",result:"passed",cases:$cases,provider_loopback_only:true,provider_hits:18,anonymous_and_valid_token_parity:true,postgresql_and_valkey_isolated:true,provider:$provider}'
