#!/usr/bin/env bash
set -euo pipefail

schema_check_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$schema_check_root"

# The generated sqlc code is committed, so nothing forces it to match its input.
# It drifted once already: internal/store/sqlcgen held SQL and Go types that had
# been edited by hand, and regenerating broke the build in twelve places. Running
# the generator and requiring an empty diff is what stops that recurring.
go tool sqlc generate
if ! git diff --quiet -- internal/store/sqlcgen; then
	echo "sqlc generate changed internal/store/sqlcgen; commit the regenerated code" >&2
	git --no-pager diff --stat -- internal/store/sqlcgen >&2
	exit 1
fi

atct_db_path="${HOME}/.atct/atct.db"
if [[ ! -f "$atct_db_path" ]]; then
	exit 0
fi

schema_check_dir="$(mktemp -d "${TMPDIR:-/tmp}/atct-schema-check.XXXXXX")"
schema_check_db="${schema_check_dir}/atct.db"

cleanup() {
	rm -f "$schema_check_db"
	rmdir "$schema_check_dir"
}
trap cleanup EXIT

schema_check_sql_path="${schema_check_db//\'/\'\'}"
sqlite3 "$atct_db_path" "VACUUM INTO '$schema_check_sql_path'"

ATCT_DRIFT_DB="$schema_check_db" go test -count=1 -run '^TestSchemaParityDrift$' ./internal/store/
