#!/usr/bin/env bash
# Copyright (c) 2026 The terraform-provider-rustfs contributors
# SPDX-License-Identifier: Apache-2.0
#
# Fail when the merged (unit + acceptance) statement coverage drops below the
# floor recorded in .github/coverage-baseline.txt.
#
# The floor exists so that a pull request cannot quietly make the project less
# tested than it was: the `Coverage` job runs this on every PR and the `latest`
# cell of the acceptance matrix repeats it on `main`.
#
# Usage: scripts/coverage-gate.sh [profile] [baseline-file]
#        scripts/coverage-gate.sh coverage-union.out
set -euo pipefail

profile="${1:-coverage-union.out}"
baseline="${2:-.github/coverage-baseline.txt}"

if [[ ! -f "$profile" ]]; then
  echo "coverage-gate: profile '$profile' not found" >&2
  exit 2
fi
if [[ ! -f "$baseline" ]]; then
  echo "coverage-gate: baseline '$baseline' not found" >&2
  exit 2
fi

floor_total=""
floor_provider=""
floor_pkg_rustfs=""
target=""
gold=""
while IFS='=' read -r key value; do
  key="$(printf '%s' "$key" | tr -d '[:space:]')"
  value="$(printf '%s' "$value" | tr -d '[:space:]')"
  case "$key" in
    floor_total) floor_total="$value" ;;
    floor_provider) floor_provider="$value" ;;
    floor_pkg_rustfs) floor_pkg_rustfs="$value" ;;
    target) target="$value" ;;
    gold) gold="$value" ;;
    "" | \#*) ;;
  esac
done < "$baseline"

if [[ -z "$floor_total" ]]; then
  echo "coverage-gate: '$baseline' defines no floor_total" >&2
  exit 2
fi

# Same aggregation as scripts/coverage-summary.sh: one row per package, plus a
# total. The profile line is
#   github.com/x/y/provider/foo.go:12.34,56.78 3 1
# (range, statements, hits).
module="$(go list -m 2>/dev/null || true)"
rows="$(awk -v mod="$module" '
  /^mode:/ { next }
  NF < 3 { next }
  {
    file = $1
    sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", file)
    pkg = file
    if (sub(/\/[^\/]+$/, "", pkg) == 0) pkg = "."
    if (mod != "" && index(pkg, mod) == 1) pkg = substr(pkg, length(mod) + 1)
    sub(/^\//, "", pkg)
    if (pkg == "") pkg = "."
    statements[pkg] += $2
    if ($3 + 0 > 0) covered[pkg] += $2
    total_statements += $2
    if ($3 + 0 > 0) total_covered += $2
  }
  END {
    for (pkg in statements) {
      if (statements[pkg] > 0) printf "%s\t%.2f\n", pkg, 100 * covered[pkg] / statements[pkg]
    }
    if (total_statements > 0) printf "total\t%.2f\n", 100 * total_covered / total_statements
  }
' "$profile")"

# Scope -> floor, in the order they are reported. `total` is always last.
scope_key() {
  case "$1" in
    pkg/rustfs) printf '%s' "pkg_rustfs" ;;
    provider) printf '%s' "provider" ;;
    total) printf '%s' "total" ;;
    *) printf '%s' "" ;;
  esac
}

floor_value() {
  case "$1" in
    pkg_rustfs) printf '%s' "$floor_pkg_rustfs" ;;
    provider) printf '%s' "$floor_provider" ;;
    total) printf '%s' "$floor_total" ;;
    *) printf '%s' "" ;;
  esac
}

below=0
detail=""
report() {
  local scope="$1" actual="$2"
  local key floor delta status
  key="$(scope_key "$scope")"
  [[ -z "$key" ]] && return 0
  floor="$(floor_value "$key")"
  [[ -z "$floor" ]] && return 0

  if awk -v a="$actual" -v b="$floor" 'BEGIN { exit !(a + 0 >= b + 0) }'; then
    status="ok"
  else
    status="below"
    below=$((below + 1))
  fi
  delta="$(awk -v a="$actual" -v b="$floor" 'BEGIN { printf "%+.1f", a - b }')"

  # shellcheck disable=SC2016  # the backticks and %% are literal Markdown and format text
  printf '| `%s` | %.1f%% | %s%% | %s |\n' "$scope" "$actual" "$floor" "$delta"
  if [[ "$status" == "below" ]]; then
    printf -v line '  - %s: %.1f%% is below the %s%% floor\n' "$scope" "$actual" "$floor"
    detail="${detail}${line}"
  fi
}

echo "Merged statement coverage (unit + acceptance)"
echo
echo "| Scope | Coverage | Floor | Delta |"
echo "|-------|----------|-------|-------|"
while IFS=$'\t' read -r scope actual; do
  [[ -z "$scope" ]] && continue
  [[ "$scope" == "total" ]] && continue
  report "$scope" "$actual"
done <<< "$rows"
total_row="$(printf '%s\n' "$rows" | awk -F'\t' '$1 == "total" { print $2 }')"
report "total" "$total_row"

echo
if [[ "$below" -gt 0 ]]; then
  echo "coverage-gate: FAIL — merged coverage dropped below the floor in .github/coverage-baseline.txt"
  printf '%b' "$detail"
  echo
  echo "Add tests for what you changed, or (only if the drop is deliberate and justified)"
  echo "lower the floor in .github/coverage-baseline.txt and run 'make coverage-readme'."
  exit 1
fi

echo "coverage-gate: PASS — every scope is at or above its floor."
if [[ -n "$target" ]]; then
  if awk -v a="$total_row" -v t="$target" 'BEGIN { exit !(a + 0 >= t + 0) }'; then
    echo "The ${target}% milestone is met."
  else
    echo "Milestones tracked in the issue tracker: ${target}% first, then ${gold:-90}%."
  fi
fi
