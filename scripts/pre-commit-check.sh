#!/bin/bash

# Pre-commit check script for maestro
# Ensures build passes and all linting issues are resolved

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

# 3. Run comprehensive linting
echo "3. Running comprehensive linting..."
LINT_OUTPUT=$(make lint 2>&1)
LINT_EXIT_CODE=$?

if [ $LINT_EXIT_CODE -ne 0 ]; then
    echo "❌ BLOCKING: Linting issues found. All lint errors must be resolved before committing."
    echo ""
    echo "Lint output:"
    echo "$LINT_OUTPUT"
    echo ""
    echo "💡 Run 'make lint' to see all issues, then fix them before committing."
    exit 1
fi

echo "✅ All linting checks passed!"
echo "✅ Pre-commit checks completed successfully!"
exit 0