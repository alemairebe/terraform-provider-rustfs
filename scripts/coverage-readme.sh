#!/usr/bin/env bash
# Copyright (c) 2026 The terraform-provider-rustfs contributors
# SPDX-License-Identifier: Apache-2.0
#
# Regenerate the coverage badge and the coverage block in README.md from
# .github/coverage-baseline.txt.
#
# The README block is generated, not hand-written, so the number in the README
# cannot drift from the number the gate enforces: the `Coverage` job runs
# `coverage-readme.sh --check` and fails when they disagree.
#
# Usage:
#   scripts/coverage-readme.sh            # rewrite README.md in place
#   scripts/coverage-readme.sh --check    # exit 1 when README.md is out of date
set -euo pipefail

check=0
readme="README.md"
baseline=".github/coverage-baseline.txt"
positional=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) check=1 ;;
    -*) echo "coverage-readme: unknown flag '$1'" >&2; exit 2 ;;
    *)
      case "$positional" in
        0) readme="$1" ;;
        1) baseline="$1" ;;
        *) echo "coverage-readme: too many arguments" >&2; exit 2 ;;
      esac
      positional=$((positional + 1))
      ;;
  esac
  shift
done

[[ -f "$readme" ]] || { echo "coverage-readme: '$readme' not found" >&2; exit 2; }
[[ -f "$baseline" ]] || { echo "coverage-readme: '$baseline' not found" >&2; exit 2; }

measured_total=""
measured_provider=""
measured_pkg_rustfs=""
floor_total=""
floor_provider=""
floor_pkg_rustfs=""
measured_commit=""
measured_date=""
target=""
gold=""
while IFS='=' read -r key value; do
  key="$(printf '%s' "$key" | tr -d '[:space:]')"
  value="$(printf '%s' "$value" | tr -d '[:space:]')"
  case "$key" in
    measured_total) measured_total="$value" ;;
    measured_provider) measured_provider="$value" ;;
    measured_pkg_rustfs) measured_pkg_rustfs="$value" ;;
    floor_total) floor_total="$value" ;;
    floor_provider) floor_provider="$value" ;;
    floor_pkg_rustfs) floor_pkg_rustfs="$value" ;;
    measured_commit) measured_commit="$value" ;;
    measured_date) measured_date="$value" ;;
    target) target="$value" ;;
    gold) gold="$value" ;;
    "" | \#*) ;;
  esac
done < "$baseline"

if [[ -z "$measured_total" || -z "$floor_total" ]]; then
  echo "coverage-readme: '$baseline' needs measured_total and floor_total" >&2
  exit 2
fi

bar() {
  awk -v pct="$1" -v width="${2:-20}" 'BEGIN {
    filled = int(pct * width / 100 + 0.5)
    if (filled > width) filled = width
    s = ""
    for (i = 0; i < filled; i++) s = s "\342\226\210"
    for (i = filled; i < width; i++) s = s "\342\226\221"
    print s
  }'
}

color() {
  awk -v pct="$1" 'BEGIN {
    if (pct >= 90) print "brightgreen"
    else if (pct >= 80) print "green"
    else if (pct >= 70) print "yellow"
    else if (pct >= 60) print "orange"
    else print "red"
  }'
}

milestone() {
  awk -v pct="$1" -v target="${2:-80}" -v gold="${3:-90}" 'BEGIN {
    if (pct >= gold) printf "milestones %s%% and %s%% met", target, gold
    else if (pct >= target) printf "%s%% met; %s%% next", target, gold
    else printf "-> %s%%, then %s%%", target, gold
  }'
}

badge_file="$(mktemp)"
block_file="$(mktemp)"
trap 'rm -f "$badge_file" "$block_file"' EXIT

printf '[![Coverage](https://img.shields.io/badge/coverage-%s%%25-%s)](#testing-and-coverage)\n' \
  "$measured_total" "$(color "$measured_total")" > "$badge_file"

{
  echo '```'
  printf 'pkg/rustfs  %s  %s%%   floor %s%%\n' "$(bar "$measured_pkg_rustfs")" "$measured_pkg_rustfs" "$floor_pkg_rustfs"
  printf 'provider    %s  %s%%   floor %s%%\n' "$(bar "$measured_provider")" "$measured_provider" "$floor_provider"
  printf 'total       %s  %s%%   floor %s%%   %s\n' \
    "$(bar "$measured_total")" "$measured_total" "$floor_total" \
    "$(milestone "$measured_total" "${target:-80}" "${gold:-90}")"
  echo '```'
  echo
  if [[ -n "$measured_commit" ]]; then
    # shellcheck disable=SC2016  # the backtick is literal Markdown
    printf 'Measured on `%s` (unit + acceptance) on %s.\n' "$measured_commit" "${measured_date:-unknown}"
  fi
} > "$block_file"

splice() {
  local start="$1" end="$2" blockfile="$3" infile="$4" outfile="$5"
  awk -v start="$start" -v end="$end" -v blockfile="$blockfile" '
    $0 == start { print; while ((getline line < blockfile) > 0) print line; close(blockfile); inblock = 1; next }
    $0 == end { inblock = 0; print; next }
    inblock { next }
    { print }
  ' "$infile" > "$outfile"
}

if ! grep -qF '<!-- coverage-badge:start -->' "$readme"; then
  echo "coverage-readme: '$readme' has no <!-- coverage-badge:start --> marker" >&2
  exit 2
fi
if ! grep -qF '<!-- coverage:start -->' "$readme"; then
  echo "coverage-readme: '$readme' has no <!-- coverage:start --> marker" >&2
  exit 2
fi

step1="$(mktemp)"
step2="$(mktemp)"
trap 'rm -f "$badge_file" "$block_file" "$step1" "$step2"' EXIT
splice '<!-- coverage-badge:start -->' '<!-- coverage-badge:end -->' "$badge_file" "$readme" "$step1"
splice '<!-- coverage:start -->' '<!-- coverage:end -->' "$block_file" "$step1" "$step2"

if [[ "$check" -eq 1 ]]; then
  if ! diff -u "$readme" "$step2" >/dev/null; then
    echo "coverage-readme: README.md is out of date with $baseline" >&2
    diff -u "$readme" "$step2" | head -40 >&2 || true
    echo >&2
    echo "Run 'make coverage-readme' and commit the result." >&2
    exit 1
  fi
  echo "coverage-readme: '$readme' is in sync with $baseline"
  exit 0
fi

# `cat` rather than `mv` so the existing file mode and ownership are kept.
cat "$step2" > "$readme"
echo "coverage-readme: updated '$readme' from $baseline"
