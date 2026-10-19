#!/usr/bin/env bash
# Test: reaper Step 6 — session-bead pruning selection and SQL cascade.
#
# Acceptance criteria:
#   1. No GC_REAPER_SESSION_BEAD_PATTERN set → the real line-56 default selects
#      the type-safe SQL path (dolt called, bd NOT called).
#   2. GC_REAPER_SESSION_BEAD_PATTERN="" → SQL path (dolt called, bd NOT called).
#   3. GC_REAPER_SESSION_BEAD_PATTERN="gm-*" → bd prune path with --pattern gm-*.
#   4. SQL path deletes labels, dependencies, events and comments for the pruned
#      session beads, in that order, before the issues rows, in one DOLT_COMMIT
#      batch.
#   5. GC_REAPER_DRY_RUN set → the SQL path sends no DELETE statements.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REAPER="$SCRIPT_DIR/../internal/bootstrap/packs/core/assets/scripts/reaper.sh"
FAILED=0

pass() { printf '\033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$1"; FAILED=1; }

if [ ! -f "$REAPER" ]; then
    printf 'ERROR: reaper.sh not found at %s\n' "$REAPER" >&2
    exit 1
fi

# Extract Step 6 block from reaper.sh.
# Uses depth-counting on column-0 if/fi to capture the complete outer block;
# blank lines inside the block are preserved correctly.
STEP6=$(awk '
  /^# Step 6:/{found=1; depth=0}
  found && /^if[[:space:]]/{depth++}
  found{
    print
    if(/^fi$/) {
      depth--
      if(depth<=0) {found=0; exit}
    }
  }
' "$REAPER")

# The real session-pattern default, exactly as reaper.sh declares it. Running
# this line (not a copy of it) is what makes the default testable.
SESSION_DEFAULT_LINE=$(grep -m1 '^SESSION_BEAD_PATTERN=' "$REAPER")
if [ -z "$STEP6" ] || [ -z "$SESSION_DEFAULT_LINE" ]; then
    printf 'ERROR: could not extract Step 6 or the SESSION_BEAD_PATTERN default from reaper.sh\n' >&2
    exit 1
fi

# Results of the most recent run_step6 call.
RUN_BD=""
RUN_DOLT=""
RUN_BD_ARGS=""
RUN_SQL=""

# run_step6 <env-pattern|UNSET> <dry-run|empty>
# Runs the real Step 6 block with the real SESSION_BEAD_PATTERN default line.
# bd and dolt_sql are stubs: the first SELECT of session ids returns one
# session, later SELECTs return none so the loop ends. Every SQL statement
# the reaper sends is recorded. Results land in the RUN_* variables.
run_step6() {
    local env_pattern="$1" dry_run="$2"
    local tmpdir
    tmpdir=$(mktemp -d)
    mkdir -p "$tmpdir/.beads"
    printf '{"dolt_database":"test_db"}' > "$tmpdir/.beads/metadata.json"
    printf '%s\n' "$STEP6" > "$tmpdir/step6.sh"
    printf '%s\n' "$SESSION_DEFAULT_LINE" > "$tmpdir/default.sh"

    if [ "$env_pattern" = "UNSET" ]; then
        printf 'unset GC_REAPER_SESSION_BEAD_PATTERN\n' > "$tmpdir/env.sh"
    else
        printf "export GC_REAPER_SESSION_BEAD_PATTERN='%s'\n" "$env_pattern" > "$tmpdir/env.sh"
    fi

    cat > "$tmpdir/run.sh" << RUNEOF
#!/usr/bin/env bash
set -euo pipefail
bd()            { printf '%s\n' "\$*" > '$tmpdir/bd_args'; touch '$tmpdir/bd_called'; printf '{"pruned_count":3}'; }
# The reaper reaches bd through "gc bd" and calls "gc session prune" directly.
# Stub gc so the test never runs the real binary against a temporary city.
gc()            {
    if [ "\$1" = "bd" ]; then
        shift
        bd "\$@"
    else
        printf '%s\n' "\$*" >> '$tmpdir/gc_calls'
        printf '{"count":0}'
    fi
}
dolt_sql()      {
    touch '$tmpdir/dolt_called'
    local q="" arg
    for arg in "\$@"; do q="\$arg"; done
    printf '%s\n' "\$q" >> '$tmpdir/sql'
    if [[ "\$q" == *"SELECT id FROM issues"* ]]; then
        if [ -f '$tmpdir/select_done' ]; then
            printf 'id\n'
        else
            touch '$tmpdir/select_done'
            printf 'id\nsess-1\n'
        fi
    elif [[ "\$q" == *"SELECT COUNT"* ]]; then
        printf 'count\n1\n'
    fi
}
record_anomaly(){ :; }
export -f bd gc dolt_sql record_anomaly
. '$tmpdir/env.sh'
CITY_ABS='$tmpdir'
CITY_BEADS_DIR='$tmpdir/.beads'
. '$tmpdir/default.sh'
SESSION_PURGE_AGE='720h'
DRY_RUN='$dry_run'
TOTAL_SESSIONS_PRUNED=0
SESSION_PRUNE_ATTEMPTED=0
CITY_DB='test_db'
. '$tmpdir/step6.sh'
RUNEOF

    bash "$tmpdir/run.sh" 2>/dev/null || true

    RUN_BD=$([ -f "$tmpdir/bd_called" ] && echo yes || echo no)
    RUN_DOLT=$([ -f "$tmpdir/dolt_called" ] && echo yes || echo no)
    RUN_BD_ARGS=$(cat "$tmpdir/bd_args" 2>/dev/null || echo "")
    RUN_SQL=$(cat "$tmpdir/sql" 2>/dev/null || echo "")
    rm -rf "$tmpdir"
}

# sql_line <fixed-text> prints the first line of RUN_SQL containing the text,
# or nothing when absent.
sql_line() {
    { printf '%s\n' "$RUN_SQL" | grep -nF -- "$1" | head -1 | cut -d: -f1; } || true
}

# ── T1: explicit empty pattern → SQL path ─────────────────────────────────────
run_step6 "" ""
if [ "$RUN_DOLT" = "yes" ] && [ "$RUN_BD" = "no" ]; then
    pass "T1: GC_REAPER_SESSION_BEAD_PATTERN='' → SQL path (dolt=yes bd=no)"
else
    fail "T1: GC_REAPER_SESSION_BEAD_PATTERN='' → expected SQL path; bd=$RUN_BD dolt=$RUN_DOLT"
fi

# ── T2: explicit gm-* pattern → bd prune path ────────────────────────────────
run_step6 "gm-*" ""
if [ "$RUN_BD" = "yes" ] && [ "$RUN_DOLT" = "no" ] \
        && printf '%s' "$RUN_BD_ARGS" | grep -qF -- '--pattern gm-*'; then
    pass "T2: GC_REAPER_SESSION_BEAD_PATTERN=gm-* → bd prune with --pattern gm-* (dolt=no)"
else
    fail "T2: GC_REAPER_SESSION_BEAD_PATTERN=gm-* → expected bd path with --pattern gm-*; bd=$RUN_BD dolt=$RUN_DOLT args=$RUN_BD_ARGS"
fi

# ── T3: unset env → real default selects SQL path ────────────────────────────
# Regression lock: the default used to be gm-*, which pruned only the legacy
# gm- prefix and never matched a city with its own bead prefix.
run_step6 "UNSET" ""
if [ "$RUN_DOLT" = "yes" ] && [ "$RUN_BD" = "no" ]; then
    pass "T3: unset GC_REAPER_SESSION_BEAD_PATTERN → default SQL path (dolt=yes bd=no)"
else
    fail "T3: unset GC_REAPER_SESSION_BEAD_PATTERN → expected SQL path default; bd=$RUN_BD dolt=$RUN_DOLT args=$RUN_BD_ARGS"
fi

# ── T4: SQL cascade removes events and comments before the issues row ────────
# Runs with an explicit empty pattern so only the cascade is under test. Each child table must be deleted for the pruned session id, in
# bd's order (labels, dependencies, events, comments), before issues.
run_step6 "" ""
line_labels=$(sql_line "DELETE FROM labels WHERE issue_id IN ('sess-1')")
line_deps=$(sql_line "DELETE FROM dependencies WHERE issue_id IN ('sess-1')")
line_events=$(sql_line "DELETE FROM events WHERE issue_id IN ('sess-1')")
line_comments=$(sql_line "DELETE FROM comments WHERE issue_id IN ('sess-1')")
line_issues=$(sql_line "DELETE FROM issues WHERE id IN ('sess-1')")
if [ -n "$line_labels" ] && [ -n "$line_deps" ] && [ -n "$line_events" ] \
        && [ -n "$line_comments" ] && [ -n "$line_issues" ] \
        && [ "$line_labels" -lt "$line_deps" ] && [ "$line_deps" -lt "$line_events" ] \
        && [ "$line_events" -lt "$line_comments" ] && [ "$line_comments" -lt "$line_issues" ]; then
    pass "T4: SQL cascade deletes labels, dependencies, events, comments, then issues for the pruned session"
else
    fail "T4: SQL cascade order/coverage wrong (labels=$line_labels deps=$line_deps events=$line_events comments=$line_comments issues=$line_issues); sql: $RUN_SQL"
fi

# ── T5: dry run sends no DELETE statements ───────────────────────────────────
run_step6 "" "1"
if [ "$RUN_DOLT" = "yes" ] && ! printf '%s' "$RUN_SQL" | grep -qF "DELETE FROM"; then
    pass "T5: GC_REAPER_DRY_RUN → counts only, no DELETE sent"
else
    fail "T5: dry run sent DELETE or skipped the count; dolt=$RUN_DOLT sql: $RUN_SQL"
fi

[ "$FAILED" -eq 0 ] && exit 0 || exit 1
