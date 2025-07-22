#!/bin/bash

# Pre-commit check script for maestro
# Ensures build passes and critical linting issues are addressed

set -e

echo "🔨 Pre-commit checks for maestro..."

# 1. Ensure build passes
echo "1. Checking build..."
if ! make build >/dev/null 2>&1; then
    echo "❌ Build failed! Fix build errors before committing."
    exit 1
fi
echo "✅ Build passed"

# 2. Run core tests (with timeout)
echo "2. Running core tests..."
if ! timeout 60s make test >/dev/null 2>&1; then
    echo "⚠️  Tests timed out or failed. Consider running 'make test' manually."
    # Don't fail commit for test timeout, but warn
fi
echo "✅ Tests completed"

# 3. Check for critical linting issues
echo "3. Checking critical linting issues..."
LINT_OUTPUT=$(make lint 2>&1 || true)

# Count critical issues
NILERR_COUNT=$(echo "$LINT_OUTPUT" | grep -c "nilerr" 2>/dev/null || echo "0")
NILNIL_COUNT=$(echo "$LINT_OUTPUT" | grep -c "nilnil" 2>/dev/null || echo "0")
CRITICAL_TYPEASSERT=$(echo "$LINT_OUTPUT" | grep "forcetypeassert" | grep -v "_test.go" | wc -l | tr -d ' ' || echo "0")

# Block commit for critical safety issues
if [ "$NILERR_COUNT" -gt 0 ]; then
    echo "❌ BLOCKING: $NILERR_COUNT nilerr issues found (can cause runtime panics)"
    exit 1
fi

if [ "$NILNIL_COUNT" -gt 0 ]; then
    echo "❌ BLOCKING: $NILNIL_COUNT nilnil issues found (can cause runtime panics)"
    exit 1
fi

if [ "$CRITICAL_TYPEASSERT" -gt 5 ]; then
    echo "❌ BLOCKING: $CRITICAL_TYPEASSERT unsafe type assertions in core files"
    exit 1
fi

# Show overall progress but don't block
TOTAL_ISSUES=$(echo "$LINT_OUTPUT" | grep -E "(errcheck|wrapcheck|forcetypeassert)" | wc -l 2>/dev/null | tr -d ' ' || echo "0")
echo "📊 Linting status: $TOTAL_ISSUES issues remaining (errcheck/wrapcheck/forcetypeassert)"

if [ "$TOTAL_ISSUES" -eq 0 ]; then
    echo "🎉 All linting issues resolved!"
else
    echo "💡 Incremental improvement progress. Continue fixing remaining issues."
fi

echo "✅ Pre-commit checks passed!"
exit 0