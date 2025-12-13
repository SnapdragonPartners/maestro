#!/bin/bash
# lint-state-access.sh - Detect common state access anti-patterns
#
# Checks for:
# 1. Raw type assertions on state data (should use utils.SafeAssert or utils.GetStateValueOr)
# 2. Magic strings in state access (should use StateKey* constants)
# 3. Direct map access instead of type-safe utilities
#
# Usage: ./scripts/lint-state-access.sh [path]
# Default path: ./pkg

set -euo pipefail

# Colors for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

SEARCH_PATH="${1:-./pkg}"
ISSUES_FOUND=0

echo "🔍 Scanning for state access anti-patterns in $SEARCH_PATH..."
echo ""

# ------------------------------------------------------------------------------
# Pattern 1: Raw type assertions on stateData or GetStateData()
# ------------------------------------------------------------------------------
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Pattern 1: Raw type assertions on state data"
echo "  Anti-pattern: stateData[\"key\"].(type) or GetStateData()[\"key\"].(type)"
echo "  Fix: Use utils.GetStateValueOr[T]() or utils.GetStateValue[T]()"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Pattern: stateData["something"].(type) or GetStateData()["something"].(type)
# Exclude test files from critical issues
RAW_ASSERTIONS=$(grep -rn --include="*.go" --exclude="*_test.go" -E 'stateData\["[^"]+"\]\.\([a-z]|GetStateData\(\)\["[^"]+"\]\.\([a-z]' "$SEARCH_PATH" 2>/dev/null || true)

if [ -n "$RAW_ASSERTIONS" ]; then
    echo -e "${RED}Found raw type assertions:${NC}"
    echo "$RAW_ASSERTIONS" | while read -r line; do
        echo -e "  ${YELLOW}$line${NC}"
    done
    ISSUES_FOUND=$((ISSUES_FOUND + $(echo "$RAW_ASSERTIONS" | wc -l)))
    echo ""
else
    echo -e "${GREEN}✓ No raw type assertions on state data found${NC}"
    echo ""
fi

# ------------------------------------------------------------------------------
# Pattern 2: Magic strings in SetStateData calls
# ------------------------------------------------------------------------------
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Pattern 2: Magic strings in SetStateData calls"
echo "  Anti-pattern: SetStateData(\"some_key\", value)"
echo "  Fix: Use SetStateData(StateKeySomeKey, value) with defined constant"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Pattern: SetStateData("lowercase_key" - but not StateKey constants
MAGIC_SETSTATE=$(grep -rn --include="*.go" -E 'SetStateData\("[a-z_]+"' "$SEARCH_PATH" 2>/dev/null || true)

if [ -n "$MAGIC_SETSTATE" ]; then
    echo -e "${YELLOW}Found magic strings in SetStateData (review needed):${NC}"
    echo "$MAGIC_SETSTATE" | while read -r line; do
        echo -e "  ${YELLOW}$line${NC}"
    done
    # Count but don't fail - some magic strings may be intentional
    MAGIC_COUNT=$(echo "$MAGIC_SETSTATE" | wc -l)
    echo ""
    echo -e "  ${YELLOW}Found $MAGIC_COUNT instances - review if constants should be defined${NC}"
    echo ""
else
    echo -e "${GREEN}✓ No magic strings in SetStateData found${NC}"
    echo ""
fi

# ------------------------------------------------------------------------------
# Pattern 3: Direct map field access with type assertions on effect data
# ------------------------------------------------------------------------------
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Pattern 3: Raw type assertions on effect/map data"
echo "  Anti-pattern: effectData[\"key\"].(type) or someMap[\"key\"].(type)"
echo "  Fix: Use utils.GetMapFieldOr[T]() or utils.SafeAssert[T]()"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Pattern: effectData["something"].(type) - common in tool effect processing
# Exclude test files from critical issues
EFFECT_ASSERTIONS=$(grep -rn --include="*.go" --exclude="*_test.go" -E 'effectData\["[^"]+"\]\.\([a-z]' "$SEARCH_PATH" 2>/dev/null || true)

if [ -n "$EFFECT_ASSERTIONS" ]; then
    echo -e "${RED}Found raw type assertions on effect data:${NC}"
    echo "$EFFECT_ASSERTIONS" | while read -r line; do
        echo -e "  ${YELLOW}$line${NC}"
    done
    ISSUES_FOUND=$((ISSUES_FOUND + $(echo "$EFFECT_ASSERTIONS" | wc -l)))
    echo ""
else
    echo -e "${GREEN}✓ No raw type assertions on effect data found${NC}"
    echo ""
fi

# ------------------------------------------------------------------------------
# Pattern 4: Using .(map[string]any) instead of SafeAssert
# ------------------------------------------------------------------------------
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Pattern 4: Raw map type assertions"
echo "  Anti-pattern: value.(map[string]any)"
echo "  Fix: Use utils.SafeAssert[map[string]any](value)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Pattern: .(map[string]any) without SafeAssert context
# Exclude lines that already use SafeAssert
MAP_ASSERTIONS=$(grep -rn --include="*.go" -E '\.\(map\[string\]any\)' "$SEARCH_PATH" 2>/dev/null | grep -v "SafeAssert" || true)

if [ -n "$MAP_ASSERTIONS" ]; then
    echo -e "${YELLOW}Found raw map type assertions (review needed):${NC}"
    echo "$MAP_ASSERTIONS" | while read -r line; do
        echo -e "  ${YELLOW}$line${NC}"
    done
    echo ""
else
    echo -e "${GREEN}✓ No raw map type assertions found${NC}"
    echo ""
fi

# ------------------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------------------
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Summary"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if [ $ISSUES_FOUND -gt 0 ]; then
    echo -e "${RED}Found $ISSUES_FOUND critical issues that should be fixed${NC}"
    echo ""
    echo "Recommended fixes:"
    echo "  1. Replace stateData[\"key\"].(T) with utils.GetStateValueOr[T](stateMachine, key, default)"
    echo "  2. Replace effectData[\"key\"].(T) with utils.GetMapFieldOr[T](effectData, key, default)"
    echo "  3. Define StateKey* constants for frequently used state keys"
    echo "  4. Use utils.SafeAssert[T](value) for complex type assertions"
    echo ""
    exit 1
else
    echo -e "${GREEN}✓ No critical issues found${NC}"
    echo ""
    echo "Note: Some patterns flagged as 'review needed' may be intentional."
    echo "Review yellow warnings and define constants where appropriate."
    echo ""
    exit 0
fi
