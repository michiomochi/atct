#!/usr/bin/env bash
set -euo pipefail

atct_db_path="${HOME}/.atct/atct.db"
if [[ ! -f "$atct_db_path" ]]; then
	exit 0
fi

schema_check_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
schema_check_dir="$(mktemp -d "${TMPDIR:-/tmp}/atct-schema-check.XXXXXX")"
schema_check_db="${schema_check_dir}/atct.db"

cleanup() {
	rm -f "$schema_check_db"
	rmdir "$schema_check_dir"
}
trap cleanup EXIT

schema_check_sql_path="${schema_check_db//\'/\'\'}"
sqlite3 "$atct_db_path" "VACUUM INTO '$schema_check_sql_path'"

cd "$schema_check_root"
ATCT_DRIFT_DB="$schema_check_db" go test -count=1 -run '^TestSchemaParityDrift$' ./internal/store/
