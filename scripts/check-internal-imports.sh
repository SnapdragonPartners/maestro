#!/bin/bash

# Check for accidental internal imports from outside pkg/agent
echo "🔍 Checking for improper internal imports..."

VIOLATIONS=$(find . -name "*.go" -not -path "./pkg/agent/*" -exec grep -l "orchestrator/pkg/agent/internal" {} \; 2>/dev/null)

if [ -n "$VIOLATIONS" ]; then
    echo "❌ Found improper internal imports:"
    echo "$VIOLATIONS"
    echo ""
    echo "Files importing pkg/agent/internal from outside pkg/agent:"
    for file in $VIOLATIONS; do
        echo "  📁 $file:"
        grep -n "orchestrator/pkg/agent/internal" "$file" | sed 's/^/    /'
    done
    exit 1
else
    echo "✅ No improper internal imports found"
fi