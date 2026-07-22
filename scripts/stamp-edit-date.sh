#!/bin/bash
# Stamp `edit_date` on staged Markdown docs that carry TOML front-matter.
#
# ADR 0017 requires every draft/live/deprecated doc to carry an accurate
# `edit_date`. Keeping that current by hand fails reliably: the author updates
# the document they are thinking about and misses the ones the change ripples
# into (indexes, plans, referencing ADRs). This stamps them mechanically at
# commit time instead.
#
# Rules:
#   - Only staged files (added/copied/modified/renamed) ending in .md.
#   - Only files that OPEN with a TOML front-matter block and carry an
#     `edit_date` key inside it. Edits are confined to that block, so an
#     `edit_date = "..."` appearing in body text or a fenced example (ADR 0017
#     documents the schema, for instance) is never rewritten. It never *adds*
#     the key, so it cannot invent front-matter.
#   - Idempotent: already-current files are left alone.
#   - A file with BOTH staged and unstaged changes is skipped with a warning.
#     Re-adding it would sweep the unstaged hunks into the commit, which is a
#     far worse failure than a stale date.
#   - Never blocks a commit. This is a convenience, not a gate: a stale date is
#     a documentation nit, while a hook that refuses to commit is a work stopper.
#
# Run standalone to preview what a commit would stamp:  scripts/stamp-edit-date.sh

set -uo pipefail

# git reports staged paths relative to the repo root, so read them from there —
# otherwise a run from a subdirectory silently fails every file read.
root="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
cd "$root" || exit 0

TODAY="$(date +%Y-%m-%d)"

# Staged .md files. -z/read -d handles paths with spaces.
staged=()
while IFS= read -r -d '' f; do staged+=("$f"); done < <(
    git diff --cached --name-only --diff-filter=ACMR -z -- '*.md' 2>/dev/null
)
[ ${#staged[@]} -eq 0 ] && exit 0

# Files with unstaged modifications — unsafe to re-add.
dirty=""
while IFS= read -r -d '' f; do dirty="${dirty}${f}"$'\n'; done < <(
    git diff --name-only -z -- '*.md' 2>/dev/null
)

# Read the front-matter edit_date: only inside the opening +++ ... +++ block.
frontmatter_date() {
    awk '
        NR == 1 && $0 != "+++" { exit }          # no front-matter: not a doc
        /^\+\+\+$/ { fm++; if (fm == 2) exit; next }
        fm == 1 && /^edit_date = "/ {
            match($0, /"[^"]*"/)
            print substr($0, RSTART + 1, RLENGTH - 2)
            exit
        }
    ' "$1"
}

stamped=0
skipped=0

for f in "${staged[@]}"; do
    [ -f "$f" ] || continue

    current="$(frontmatter_date "$f")"
    [ -n "$current" ] || continue
    [ "$current" = "$TODAY" ] && continue

    if printf '%s' "$dirty" | grep -qxF "$f"; then
        echo "   ⚠️  $f has unstaged changes — not stamping (would pull them into the commit); update edit_date by hand"
        skipped=$((skipped + 1))
        continue
    fi

    # Temp file alongside the target: mktemp's default dir may be a different
    # filesystem, which makes the mv below a cross-device failure.
    tmp="$(mktemp "${f}.stampXXXXXX")" || continue

    if awk -v today="$TODAY" '
        BEGIN { fm = 0; done = 0 }
        /^\+\+\+$/ { fm++ }
        !done && fm == 1 && /^edit_date = "/ {
            sub(/"[^"]*"/, "\"" today "\"")
            done = 1
        }
        { print }
    ' "$f" > "$tmp" && [ -s "$tmp" ] && mv "$tmp" "$f"; then
        git add -- "$f"
        echo "   📅 $f: $current → $TODAY"
        stamped=$((stamped + 1))
    else
        rm -f "$tmp"
        echo "   ⚠️  $f: could not stamp (left unchanged)"
    fi
done

if [ "$stamped" -gt 0 ] || [ "$skipped" -gt 0 ]; then
    echo "   edit_date: $stamped stamped, $skipped skipped"
fi
exit 0
