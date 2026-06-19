#!/usr/bin/env bash
#
# Litescope demo runner — drives a recorded walkthrough.
#
#   1. (Re)generate demo data:   python3 setup.py
#   2. Build the binary:         go build -o /tmp/litescope ../cmd/litescope
#   3. Record + run:             ./demo.sh
#
# Each step pauses for ENTER so you can narrate while recording.
# See SCRIPT.md for the spoken narration that pairs with each step.

# Note: no `set -e`. Several commands (advise, blast-radius, recover) intentionally
# return a non-zero exit code when they find an issue — that's a signal, not a failure.
set -uo pipefail
cd "$(dirname "$0")"

# Pro features are gated; this test license + skip-verify unlocks them locally.
export LITESCOPE_SKIP_VERIFY=1
export LITESCOPE_LICENSE=lsc_pro_test

LS=${LITESCOPE:-/tmp/litescope}

# ── presentation helpers ──────────────────────────────────────────────
bold=$'\033[1m'; dim=$'\033[2m'; teal=$'\033[36m'; rst=$'\033[0m'

section() { printf '\n%s━━━ %s ━━━%s\n\n' "$teal$bold" "$1" "$rst"; }
# A non-zero exit from these commands is a meaningful signal, not a failure — never abort on it.
run()     { printf '%s$ %s%s\n' "$dim" "$*" "$rst"; "$@" || true; }
pause()   { printf '\n%s' "$dim"; read -rp "  ⏎ " _; printf '%s' "$rst"; }

# ── Scene 1 — the AI app database, one command ─────────────────────────
section "1 · Triage a single AI-app database"
run "$LS" health app.db
run "$LS" advise app.db
pause

# ── Scene 2 — declarative migration with blast-radius ──────────────────
section "2 · Plan a migration — schema-as-code + blast radius"
run "$LS" migrate plan app.db --schema schema.sql
pause

# ── Scene 3 — the fleet: diagnose → treat ──────────────────────────────
section "3a · Fingerprint the fleet (which schemas are really out there?)"
run "$LS" fleet fingerprint
pause

section "3b · Fleet health — find the fault"
run "$LS" fleet health
pause

section "3c · Blast radius — who else is at risk?"
run "$LS" fleet blast-radius
pause

section "3d · Converge the drift (dry-run)"
run "$LS" fleet converge --dry-run
pause

section "3e · Recover the corrupt tenant from its verified backup (dry-run)"
run "$LS" fleet recover --dry-run
pause

# ── Scene 4 — the AI calls it directly (MCP) ───────────────────────────
section "4 · The same engine over MCP — an AI agent calls it"
printf '%s$ echo tools/list | litescope mcp%s\n' "$dim" "$rst"
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | "$LS" mcp \
  | python3 -c "import sys,json; print('\n'.join('  • '+t['name'] for t in json.load(sys.stdin)['result']['tools']))"
pause

section "Done"
printf '  litescope — the DB operations layer for AI apps.\n\n'
