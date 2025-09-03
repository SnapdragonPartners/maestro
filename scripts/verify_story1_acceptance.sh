#!/usr/bin/env bash
# Verification script for Story 1 acceptance criteria

set -euo pipefail

echo "🔍 Verifying Story 1 - GitHub Auth Init Acceptance Criteria"
echo

# 1. Verify embedded script exists and contains required components
echo "✅ Checking embedded script..."
if [ -f "internal/embeds/scripts/gh_init.sh" ]; then
    echo "  - ✅ gh_init.sh file exists"
    
    # Check script has shebang
    if grep -q "^#!/usr/bin/env sh" internal/embeds/scripts/gh_init.sh; then
        echo "  - ✅ Script has proper shebang"
    else
        echo "  - ❌ Script missing shebang"
        exit 1
    fi
    
    # Check key components
    components=(
        "set -euo pipefail"
        "GH_CONFIG_DIR.*\/dev\/shm\/gh"
        "GITHUB_TOKEN.*required"
        "gh auth login"
        "gh auth setup-git"
        "git config --global user.name"
        "git config --global user.email"
        "GitHub auth configured"
    )
    
    for component in "${components[@]}"; do
        if grep -q "$component" internal/embeds/scripts/gh_init.sh; then
            echo "  - ✅ Contains: $component"
        else
            echo "  - ❌ Missing: $component"
            exit 1
        fi
    done
else
    echo "  - ❌ gh_init.sh file not found"
    exit 1
fi

echo

# 2. Verify Go embedding works
echo "✅ Checking Go embedding..."
if go build -o /tmp/test_embed internal/embeds/scripts/gh_init.go &>/dev/null; then
    echo "  - ✅ Go embedding compiles successfully"
    rm -f /tmp/test_embed
else
    echo "  - ❌ Go embedding compilation failed"
    exit 1
fi

echo

# 3. Verify Docker interface implementation
echo "✅ Checking Docker interface..."
go test ./internal/runtime -v -run TestInstallAndRunGHInit > /tmp/story1_test.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ Docker interface test passed"
    echo "  - ✅ InstallAndRunGHInit function works correctly"
else
    echo "  - ❌ Docker interface test failed"
    cat /tmp/story1_test.log
    exit 1
fi

echo

# 4. Verify embedded script test
echo "✅ Checking embedded script validation..."
go test ./internal/embeds/scripts -v > /tmp/story1_embed_test.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ Embedded script validation passed"
else
    echo "  - ❌ Embedded script validation failed"
    cat /tmp/story1_embed_test.log
    exit 1
fi

echo

# 5. Verify integration pattern
echo "✅ Checking integration pattern..."
go test ./internal/runtime -v -run TestGitHubAuthIntegrationExample > /tmp/story1_integration_test.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ Integration pattern verified"
    echo "  - ✅ Docker executor implements required interface"
else
    echo "  - ❌ Integration pattern verification failed"
    cat /tmp/story1_integration_test.log
    exit 1
fi

echo

# 6. Verify linting passes
echo "✅ Checking code quality..."
make lint > /tmp/story1_lint.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ All linting checks passed"
else
    echo "  - ❌ Linting failed"
    cat /tmp/story1_lint.log
    exit 1
fi

echo
echo "🎉 Story 1 - GitHub Auth Init Acceptance Criteria: ALL PASSED"
echo
echo "Summary of implemented features:"
echo "1. ✅ Embedded gh_init.sh script with all required components"
echo "2. ✅ Go embedding using go:embed directive"
echo "3. ✅ InstallAndRunGHInit function with proper Docker interface"
echo "4. ✅ Docker executor extended with Exec and CpToContainer methods"
echo "5. ✅ Comprehensive test coverage for all components"
echo "6. ✅ Integration pattern demonstrated and verified"
echo "7. ✅ All code passes linting and quality checks"
echo
echo "Ready for Story 2 implementation! 🚀"