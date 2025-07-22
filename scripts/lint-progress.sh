#!/bin/bash

# Lint progress tracking script
# Shows current linting status and allows incremental improvement

set -e

echo "🔍 Maestro Linting Progress Report"
echo "=================================="

# Run linter and capture output
LINT_OUTPUT=$(make lint 2>&1 | tee /tmp/lint_output.txt || true)

# Count issues by type
ERRCHECK_COUNT=$(grep -c "errcheck" /tmp/lint_output.txt || echo "0")
WRAPCHECK_COUNT=$(grep -c "wrapcheck" /tmp/lint_output.txt || echo "0") 
FORCETYPEASSERT_COUNT=$(grep -c "forcetypeassert" /tmp/lint_output.txt || echo "0")
NILERR_COUNT=$(grep -c "nilerr" /tmp/lint_output.txt || echo "0")
NILNIL_COUNT=$(grep -c "nilnil" /tmp/lint_output.txt || echo "0")
TOTAL_COUNT=$(grep -E "(errcheck|wrapcheck|forcetypeassert|nilerr|nilnil)" /tmp/lint_output.txt | wc -l)

echo "📊 Issue Breakdown:"
echo "  - errcheck (unchecked errors): $ERRCHECK_COUNT"  
echo "  - wrapcheck (unwrapped errors): $WRAPCHECK_COUNT"
echo "  - forcetypeassert (unsafe type assertions): $FORCETYPEASSERT_COUNT"
echo "  - nilerr (nil error issues): $NILERR_COUNT"
echo "  - nilnil (dual nil returns): $NILNIL_COUNT"
echo "  - TOTAL: $TOTAL_COUNT"

echo ""
echo "🎯 Priority Focus Areas:"
if [ "$NILERR_COUNT" -gt 0 ]; then
    echo "  ⚠️  HIGH: Fix nilerr issues (can cause runtime panics)"
fi
if [ "$NILNIL_COUNT" -gt 0 ]; then
    echo "  ⚠️  HIGH: Fix nilnil issues (can cause runtime panics)"
fi
if [ "$FORCETYPEASSERT_COUNT" -gt 10 ]; then
    echo "  🔶 MEDIUM: Reduce forcetypeassert issues (currently $FORCETYPEASSERT_COUNT)"
fi

echo ""
if [ "$TOTAL_COUNT" -eq 0 ]; then
    echo "✅ All critical linting issues resolved!"
    echo "🚀 Ready for clean pre-commit hook!"
else
    echo "📈 Progress made! Continue fixing remaining $TOTAL_COUNT issues."
    echo "💡 Focus on critical safety issues first (nilerr, nilnil, forcetypeassert in core files)"
fi

# Clean up
rm -f /tmp/lint_output.txt

exit 0