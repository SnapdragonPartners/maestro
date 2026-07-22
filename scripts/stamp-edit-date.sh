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
#   - Only files whose front-matter already has an `edit_date` key. Files
#     without front-matter are not docs under the convention and are skipped;
#     this never *adds* the key, so it cannot invent front-matter.
#   - Only the first `edit_date` in the file (the front-matter one).
#   - Idempotent: already-current files are left alone.
#   - A file with BOTH staged and unstaged changes is skipped with a warning.
#     Re-adding it would sweep the unstaged hunks into the commit, which is a
#     far worse failure than a stale date.
#   - Never blocks a commit. This is a convenience, not a gate: a stale date is
#     a documentation nit, while a hook that refuses to commit is a work stopper.
#
# Run standalone to preview what a commit would stamp:  scripts/stamp-edit-date.sh

set -uo pipefail

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

stamped=0
skipped=0

for f in "${staged[@]}"; do
    [ -f "$f" ] || continue

    # Must already carry an edit_date in its front-matter (first 40 lines).
    current="$(head -40 "$f" | grep -m1 '^edit_date = "' | sed 's/^edit_date = "//; s/".*$//')" || true
    [ -n "$current" ] || continue
    [ "$current" = "$TODAY" ] && continue

    if printf '%s' "$dirty" | grep -qxF "$f"; then
        echo "   ⚠️  $f has unstaged changes — not stamping (would pull them into the commit); update edit_date by hand"
        skipped=$((skipped + 1))
        continue
    fi

    tmp="$(mktemp)" || continue
    if awk -v today="$TODAY" '
        BEGIN { done = 0 }
        !done && /^edit_date = "/ { sub(/"[^"]*"/, "\"" today "\""); done = 1 }
        { print }
    ' "$f" > "$tmp" && [ -s "$tmp" ]; then
        mv "$tmp" "$f"
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
