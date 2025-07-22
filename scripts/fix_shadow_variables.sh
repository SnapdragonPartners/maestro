#!/bin/bash

# Script to automatically fix common shadow variable issues
# This focuses on the most common pattern: err variable shadowing

echo "🔧 Fixing shadow variable issues..."

# Find all Go files (excluding vendor and git directories)
find . -name "*.go" -not -path "./vendor/*" -not -path "./.git/*" | while read -r file; do
    echo "Processing: $file"
    
    # Create a backup
    cp "$file" "$file.backup"
    
    # Use awk to fix shadow variables in if statements
    # This handles patterns like: if err := someFunc(); err != nil {
    awk '
    {
        line = $0
        # Match: if err := ... err != nil
        if (match(line, /^(\s+)if err := ([^;]+); err != nil/) && prev_err_line > 0 && NR - prev_err_line < 20) {
            # Replace with a unique variable name
            gsub(/if err :=/, "if shadowErr :=", line)
            gsub(/; err != nil/, "; shadowErr != nil", line)
            print line
            shadow_replacement = 1
        }
        # Match: if err := ... { on its own line, followed by err != nil check
        else if (match(line, /^(\s+)if err := ([^{]+)\{?\s*$/) && prev_err_line > 0 && NR - prev_err_line < 20) {
            gsub(/if err :=/, "if shadowErr :=", line)
            print line
            shadow_replacement = 1
        }
        # Handle the error check line after if err := assignment
        else if (shadow_replacement == 1 && match(line, /err != nil/)) {
            gsub(/err != nil/, "shadowErr != nil", line)
            gsub(/err\)/, "shadowErr)", line)
            print line
            shadow_replacement = 0
        }
        # Handle error usage in subsequent lines after shadow replacement
        else if (shadow_replacement == 1 && match(line, /^\s+.*err/)) {
            gsub(/\berr\b/, "shadowErr", line)
            print line
            if (match(line, /}$/)) {
                shadow_replacement = 0
            }
        }
        else {
            print line
            shadow_replacement = 0
        }
        
        # Track where err is declared
        if (match(line, /^\s*[a-zA-Z_][a-zA-Z0-9_]*.*,?\s*err\s*:=/)) {
            prev_err_line = NR
        }
    }' "$file" > "$file.tmp"
    
    # Check if the file was modified
    if ! cmp -s "$file" "$file.tmp"; then
        mv "$file.tmp" "$file"
        echo "  ✅ Modified: $file"
    else
        rm "$file.tmp"
        echo "  ⚪ No changes: $file"
    fi
    
    # Clean up backup if no changes were needed
    if cmp -s "$file" "$file.backup"; then
        rm "$file.backup"
    fi
done

echo "✅ Shadow variable fixes complete"