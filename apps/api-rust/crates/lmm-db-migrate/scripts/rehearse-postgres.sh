#!/usr/bin/env bash
set -Eeuo pipefail

for command in initdb pg_ctl createdb jq psql; do
  command -v "${command}" >/dev/null || {
    echo "required PostgreSQL command is missing: ${command}" >&2
    exit 1
  }
done

crate_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
rehearsal_dir=$(mktemp -d /tmp/lmm-db-migrate-pg.XXXXXX)
port=$((55440 + RANDOM % 900))

cleanup() {
  if [[ -f "${rehearsal_dir}/postmaster.pid" ]]; then
    pg_ctl -D "${rehearsal_dir}" -m immediate stop >/dev/null 2>&1 || true
  fi
  if [[ "${rehearsal_dir}" == /tmp/lmm-db-migrate-pg.* ]]; then
    rm -rf -- "${rehearsal_dir}"
  fi
}
trap cleanup EXIT

initdb -D "${rehearsal_dir}" -A trust -U postgres --no-locale --encoding=UTF8 >/dev/null
pg_ctl -D "${rehearsal_dir}" -o "-k ${rehearsal_dir} -p ${port}" -w start >/dev/null
createdb -h "${rehearsal_dir}" -p "${port}" -U postgres lmm_rehearsal
psql -X -v ON_ERROR_STOP=1 -h "${rehearsal_dir}" -p "${port}" -U postgres \
  -d lmm_rehearsal -f "${crate_dir}/schema/postgresql-baseline.sql" >/dev/null

catalog_file="${rehearsal_dir}/postgres-catalog.json"
umask 077
psql -XAt -v ON_ERROR_STOP=1 -h "${rehearsal_dir}" -p "${port}" -U postgres \
  -d lmm_rehearsal -f "${crate_dir}/schema/export-postgres-catalog.sql" >"${catalog_file}"

catalog_tables=$(jq 'length' "${catalog_file}")
catalog_columns=$(jq '[.[].columns[]] | length' "${catalog_file}")
catalog_indexes=$(jq '[.[].indexes[]] | length' "${catalog_file}")
catalog_sequences=$(jq '[.[] | select(.sequence != null)] | length' "${catalog_file}")
[[ "${catalog_tables}" == 38 ]] || { echo "catalog export has ${catalog_tables} tables" >&2; exit 1; }
[[ "${catalog_columns}" == 467 ]] || { echo "catalog export has ${catalog_columns} columns" >&2; exit 1; }
[[ "${catalog_indexes}" == 193 ]] || { echo "catalog export has ${catalog_indexes} indexes" >&2; exit 1; }
[[ "${catalog_sequences}" == 31 ]] || { echo "catalog export has ${catalog_sequences} sequences" >&2; exit 1; }

cargo run --quiet --locked --manifest-path "${crate_dir}/../../Cargo.toml" \
  -p lmm-db-migrate -- postgres-catalog-validate \
  --manifest "${crate_dir}/schema/table-map.json" \
  --catalog "${catalog_file}"

table_count=$(psql -XAt -h "${rehearsal_dir}" -p "${port}" -U postgres -d lmm_rehearsal \
  -c "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
sequence_count=$(psql -XAt -h "${rehearsal_dir}" -p "${port}" -U postgres -d lmm_rehearsal \
  -c "SELECT count(*) FROM pg_sequences WHERE schemaname = 'public'")
unowned_sequences=$(psql -XAt -h "${rehearsal_dir}" -p "${port}" -U postgres -d lmm_rehearsal -c \
  "SELECT count(*) FROM pg_class s
   WHERE s.relkind = 'S' AND s.relnamespace = 'public'::regnamespace
     AND NOT EXISTS (
       SELECT 1 FROM pg_depend d
       WHERE d.objid = s.oid AND d.deptype = 'a'
     )")
missing_defaults=$(psql -XAt -h "${rehearsal_dir}" -p "${port}" -U postgres -d lmm_rehearsal -c \
  "SELECT count(*) FROM pg_class s
   JOIN pg_depend d ON d.objid = s.oid AND d.deptype = 'a'
   JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
   LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
   WHERE s.relkind = 'S' AND s.relnamespace = 'public'::regnamespace
     AND (a.attname <> 'id' OR pg_get_expr(ad.adbin, ad.adrelid) NOT LIKE 'nextval(%')")

[[ "${table_count}" == 38 ]] || { echo "expected 38 tables, found ${table_count}" >&2; exit 1; }
[[ "${sequence_count}" == 31 ]] || { echo "expected 31 sequences, found ${sequence_count}" >&2; exit 1; }
[[ "${unowned_sequences}" == 0 ]] || { echo "found ${unowned_sequences} unowned sequences" >&2; exit 1; }
[[ "${missing_defaults}" == 0 ]] || { echo "found ${missing_defaults} invalid sequence defaults" >&2; exit 1; }

echo "PostgreSQL baseline rehearsal passed: 38 tables, 467 columns, 193 indexes, 31 owned id sequences"

LMM_TEST_PG_SOCKET="${rehearsal_dir}" \
LMM_TEST_PG_PORT="${port}" \
LMM_TEST_PG_DATABASE=lmm_rehearsal \
  cargo test --manifest-path "${crate_dir}/../../Cargo.toml" \
    -p lmm-db-migrate --test postgres_equivalence --locked -- \
    --ignored --exact --nocapture sqlite_and_postgres_should_have_identical_canonical_table_hashes

LMM_TEST_DATABASE_URL="postgresql://postgres@/lmm_rehearsal?host=${rehearsal_dir}&port=${port}" \
  cargo test --manifest-path "${crate_dir}/../../Cargo.toml" \
    -p lmm-db-migrate --test full_copy --locked -- \
    --ignored --exact --nocapture full_copy_should_verify_all_tables_and_rollback_both_fault_phases
