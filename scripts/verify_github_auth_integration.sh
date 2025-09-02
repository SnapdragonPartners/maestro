#!/usr/bin/env bash
# Verification script for GitHub Auth Integration (Option 1 - Clean Approach)

set -euo pipefail

echo "🔍 Verifying GitHub Auth Integration - Clean Approach (Option 1)"
echo

# 1. Verify new functions exist and old ones are removed
echo "✅ Checking new GitHub auth functions..."

if grep -q "func.*setupGitHubAuthentication" pkg/coder/setup.go; then
    echo "  - ✅ New setupGitHubAuthentication function exists"
else
    echo "  - ❌ New setupGitHubAuthentication function missing"
    exit 1
fi

if grep -q "func.*verifyGitHubAuthSetup" pkg/coder/setup.go; then
    echo "  - ✅ New verifyGitHubAuthSetup function exists"
else
    echo "  - ❌ New verifyGitHubAuthSetup function missing"  
    exit 1
fi

if grep -q "func.*configureGitUserIdentity" pkg/coder/setup.go; then
    echo "  - ✅ New configureGitUserIdentity function exists"
else
    echo "  - ❌ New configureGitUserIdentity function missing"
    exit 1
fi

# Check old function is removed
if grep -q "func.*ensureGitHubAuthentication" pkg/coder/setup.go; then
    echo "  - ❌ Old ensureGitHubAuthentication function still exists (should be removed)"
    exit 1
else
    echo "  - ✅ Old ensureGitHubAuthentication function properly removed"
fi

echo

# 2. Verify embedded script integration  
echo "✅ Checking embedded script integration..."

if grep -q "scripts.GHInitSh" pkg/coder/setup.go; then
    echo "  - ✅ Embedded script is used in setup"
else
    echo "  - ❌ Embedded script not used in setup"
    exit 1
fi

if grep -q "runtime.InstallAndRunGHInit" pkg/coder/setup.go; then
    echo "  - ✅ Runtime function is called correctly"
else
    echo "  - ❌ Runtime function not called"
    exit 1
fi

echo

# 3. Verify prepare_merge uses new approach
echo "✅ Checking prepare_merge integration..."

if grep -q "verifyGitHubAuthSetup" pkg/coder/prepare_merge.go; then
    echo "  - ✅ prepare_merge uses new verification approach"
else
    echo "  - ❌ prepare_merge not updated to use new approach"
    exit 1
fi

# Check old function call is removed from prepare_merge
if grep -q "ensureGitHubAuthentication.*false" pkg/coder/prepare_merge.go; then
    echo "  - ❌ prepare_merge still calls old ensureGitHubAuthentication function"
    exit 1
else
    echo "  - ✅ prepare_merge properly updated to use new approach"
fi

echo

# 4. Verify imports are correct
echo "✅ Checking imports..."

if grep -q "orchestrator/internal/embeds/scripts" pkg/coder/setup.go; then
    echo "  - ✅ Scripts package imported"
else
    echo "  - ❌ Scripts package not imported"
    exit 1
fi

if grep -q "orchestrator/internal/runtime" pkg/coder/setup.go; then
    echo "  - ✅ Runtime package imported"
else
    echo "  - ❌ Runtime package not imported"
    exit 1
fi

echo

# 5. Verify comprehensive logging and verification
echo "✅ Checking logging and verification..."

log_checks=(
    "Setting up GitHub authentication using embedded script approach"
    "Installing and executing GitHub authentication script"
    "GitHub authentication script completed successfully"
    "Verifying GitHub authentication setup"
    "Git is available"
    "GitHub CLI is available"
    "GitHub CLI authentication verified"
    "Configuring git user identity"
    "Git user identity configured and verified"
)

for log_msg in "${log_checks[@]}"; do
    if grep -q "$log_msg" pkg/coder/setup.go; then
        echo "  - ✅ Contains logging: $log_msg"
    else
        echo "  - ❌ Missing logging: $log_msg"
        exit 1
    fi
done

echo

# 6. Verify linting passes
echo "✅ Checking code quality..."
make lint > /tmp/github_auth_lint.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ All linting checks passed"
else
    echo "  - ❌ Linting failed"
    cat /tmp/github_auth_lint.log
    exit 1
fi

echo

# 7. Verify tests still pass
echo "✅ Checking tests..."
go test ./internal/runtime ./pkg/coder -v -run "TestInstallAndRunGHInit|TestGitHubAuthIntegrationExample" > /tmp/github_auth_test.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ GitHub auth tests passed"
else
    echo "  - ❌ Tests failed"
    cat /tmp/github_auth_test.log
    exit 1
fi

echo
echo "🎉 GitHub Auth Integration Verification: ALL PASSED"
echo
echo "Summary of clean integration (Option 1):"
echo "1. ✅ Replaced old manual GitHub auth with embedded script approach"
echo "2. ✅ Added comprehensive verification and logging"
echo "3. ✅ Updated both SETUP and PREPARE_MERGE states cleanly"
echo "4. ✅ Removed redundant authentication code"
echo "5. ✅ All tests passing with proper error handling"
echo "6. ✅ Code passes all linting and quality checks"
echo
echo "Ready for Story 2 implementation with clean GitHub auth foundation! 🚀"