#!/usr/bin/env bash
# spec-status.sh — cross-reference docs/prd-*.md against openspec/changes/
# (both active and archived) and report what's been implemented, what's
# drafted but not yet built, and what has no OpenSpec change at all.
#
# A PRD and an OpenSpec change are linked by the change's proposal.md (or
# README.md as a fallback) mentioning the PRD's file path somewhere in its
# text — this project has used at least three different phrasings for that
# reference across its history ("PRD: `docs/prd-x.md`.", "`docs/prd-x.md`
# (full text), answering...", inline prose under a "## Why" heading with no
# "Requirements served" section at all), so matching is done by searching
# for the PRD's own filename as a substring, not by assuming one fixed
# label or section structure.
#
# An OpenSpec change under openspec/changes/archive/ is archived, which
# only happens after `openspec archive` folds its specs into the living
# spec set — treated here as "implemented." A change still under
# openspec/changes/ (not archived) is checked against git log on the
# current branch: if any commit message names the change's directory
# name, real work happened on it (this project's dispatch commits
# consistently say "Implement openspec change <change-id>" or "Implements
# openspec/changes/<change-id>") even though nobody ran `openspec archive`
# afterward — a real, previously-invisible gap this script exists to
# surface, not paper over. A change with neither an archive location nor
# any matching commit is genuinely not yet started.
#
# Usage: scripts/spec-status.sh [--repo-root <path>]

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
if [ "${1:-}" = "--repo-root" ]; then
  REPO_ROOT="$2"
fi
cd "$REPO_ROOT"

CHANGES_DIR="openspec/changes"
ARCHIVE_DIR="$CHANGES_DIR/archive"

# --- collect every openspec change's proposal/README text, tagged with its
# archived/active status and directory name, once — reused per PRD rather
# than re-scanned on every PRD's lookup.
declare -a change_dirs=()
declare -a change_ids=()
declare -a change_archived=()

for d in "$CHANGES_DIR"/*/; do
  name="$(basename "$d")"
  [ "$name" = "archive" ] && continue
  change_dirs+=("$d")
  change_ids+=("$name")
  change_archived+=("no")
done
if [ -d "$ARCHIVE_DIR" ]; then
  for d in "$ARCHIVE_DIR"/*/; do
    [ -d "$d" ] || continue
    name="$(basename "$d")"
    change_dirs+=("$d")
    change_ids+=("$name")
    change_archived+=("yes")
  done
fi

change_text_for() {
  local dir="$1"
  cat "$dir/proposal.md" "$dir/README.md" 2>/dev/null || true
}

implemented_by_commit() {
  local change_id="$1"
  git log --oneline --all -1 --grep="$change_id" -- . 2>/dev/null || true
}

declare -a matched_change_index=()  # tracks which changes matched at least one PRD

echo "PRD                                                    | Status                                          | OpenSpec change(s)"
echo "-------------------------------------------------------|-------------------------------------------------|--------------------"

# PRDs live at docs/prd-*.md, or docs/prd/archive/*.md once their OpenSpec
# change is fully implemented and archived (docs/README.md's own
# convention). A basename is only counted once even if (unexpectedly) it
# appears in both places.
declare -a prd_files=()
declare -A seen_prd_basenames=()
add_prd_file() {
  local f="$1"
  [ -f "$f" ] || return 0
  local base
  base="$(basename "$f")"
  if [ -n "${seen_prd_basenames[$base]:-}" ]; then
    return 0
  fi
  seen_prd_basenames["$base"]=1
  prd_files+=("$f")
  return 0
}
for f in docs/prd-*.md; do add_prd_file "$f"; done
if [ -d docs/prd/archive ]; then
  for f in docs/prd/archive/*.md; do add_prd_file "$f"; done
fi
if [ -d docs/prd ]; then
  for f in docs/prd/*.md; do add_prd_file "$f"; done
fi

for prd in "${prd_files[@]}"; do
  base="$(basename "$prd")"
  # docs/prd-sgt.md is the umbrella product PRD every satellite PRD extends,
  # not itself a single implementable change — reported separately, not as
  # a row that can be "not yet acted on."
  if [ "$base" = "prd-sgt.md" ]; then
    continue
  fi

  matches=()
  for i in "${!change_dirs[@]}"; do
    text="$(change_text_for "${change_dirs[$i]}")"
    if printf '%s' "$text" | grep -qF "$base"; then
      matches+=("$i")
    fi
  done

  if [ "${#matches[@]}" -eq 0 ]; then
    printf "%-56s | %-49s | %s\n" "$base" "No OpenSpec change yet" "-"
    continue
  fi

  statuses=()
  ids=()
  for i in "${matches[@]}"; do
    matched_change_index+=("$i")
    cid="${change_ids[$i]}"
    ids+=("$cid")
    if [ "${change_archived[$i]}" = "yes" ]; then
      statuses+=("archived/implemented")
    else
      commit="$(implemented_by_commit "$cid")"
      if [ -n "$commit" ]; then
        statuses+=("implemented, not archived")
      else
        statuses+=("drafted, not implemented")
      fi
    fi
  done

  ids_joined="$(IFS=,; echo "${ids[*]}")"
  statuses_joined="$(IFS=,; echo "${statuses[*]}")"
  printf "%-56s | %-49s | %s\n" "$base" "$statuses_joined" "$ids_joined"
done

echo ""
echo "OpenSpec changes with no matching PRD (orphans — check by hand):"
found_orphan="no"
for i in "${!change_dirs[@]}"; do
  is_matched="no"
  for m in "${matched_change_index[@]:-}"; do
    if [ "$m" = "$i" ]; then
      is_matched="yes"
      break
    fi
  done
  if [ "$is_matched" = "no" ]; then
    found_orphan="yes"
    archived_note=""
    [ "${change_archived[$i]}" = "yes" ] && archived_note=" (archived)"
    echo "  - ${change_ids[$i]}${archived_note}"
  fi
done
if [ "$found_orphan" = "no" ]; then
  echo "  (none)"
fi
