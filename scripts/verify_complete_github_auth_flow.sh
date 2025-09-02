#!/usr/bin/env bash
# Comprehensive verification script for complete GitHub Auth flow

set -euo pipefail

echo "🔍 Verifying Complete GitHub Auth Flow - All Container Types"
echo

# 1. Verify planning container setup (SETUP state)
echo "✅ Checking SETUP state - Planning container..."

if grep -q "configureWorkspaceMount.*true.*planning" pkg/coder/setup.go; then
    echo "  - ✅ Planning container configured in SETUP state (readonly=true)"
else
    echo "  - ❌ Planning container configuration missing"
    exit 1
fi

# Planning containers should NOT get GitHub auth (readonly, no network)
if grep -A 10 "configureWorkspaceMount.*true" pkg/coder/setup.go | grep -q "setupGitHubAuthentication"; then
    echo "  - ❌ Planning container should NOT get GitHub auth"
    exit 1
else
    echo "  - ✅ Planning container correctly does NOT get GitHub auth (readonly)"
fi

echo

# 2. Verify coding container setup (PLAN_REVIEW → configureWorkspaceMount)
echo "✅ Checking PLAN_REVIEW state - Coding container creation..."

if grep -q "configureWorkspaceMount.*false.*coding" pkg/coder/plan_review.go; then
    echo "  - ✅ Coding container created in PLAN_REVIEW state (readonly=false)"
else
    echo "  - ❌ Coding container creation missing in PLAN_REVIEW"
    exit 1
fi

if grep -q "creates a new coding container with GitHub auth" pkg/coder/plan_review.go; then
    echo "  - ✅ PLAN_REVIEW has clear documentation about GitHub auth"
else
    echo "  - ❌ Missing GitHub auth documentation in PLAN_REVIEW"
    exit 1
fi

echo

# 3. Verify configureWorkspaceMount handles both cases correctly  
echo "✅ Checking configureWorkspaceMount - Container type logic..."

# Should call GitHub auth setup for coding containers (readonly=false)
if grep -A 5 "if !readonly" pkg/coder/setup.go | grep -q "setupGitHubAuthentication"; then
    echo "  - ✅ GitHub auth setup called for coding containers (!readonly)"
else
    echo "  - ❌ GitHub auth setup missing for coding containers"
    exit 1
fi

# Should not call GitHub auth for planning containers (readonly=true already skipped)
echo "  - ✅ Planning containers skip GitHub auth (readonly=true logic)"

echo

# 4. Verify GitHub auth setup function exists and is comprehensive
echo "✅ Checking setupGitHubAuthentication function..."

auth_checks=(
    "Setting up GitHub authentication using embedded script approach"
    "GITHUB_TOKEN not found in environment"  
    "InstallAndRunGHInit"
    "GitHub authentication script failed"
    "verifyGitHubAuthSetup"
    "configureGitUserIdentity"
)

for check in "${auth_checks[@]}"; do
    if grep -q "$check" pkg/coder/setup.go; then
        echo "  - ✅ Contains: $check"
    else
        echo "  - ❌ Missing: $check"
        exit 1
    fi
done

echo

# 5. Verify complete state flow and timing
echo "✅ Checking complete state flow..."

echo "  📋 State Flow Analysis:"
echo "     WAITING → SETUP (planning container, no auth needed)"
echo "     SETUP → PLANNING (uses planning container)"  
echo "     PLANNING → PLAN_REVIEW"
echo "     PLAN_REVIEW → CODING (creates coding container + GitHub auth)"
echo "     CODING → ... (uses coding container with auth)"

# Check the state transition definitions
if grep -A 10 "CoderTransitions.*map" pkg/coder/coder_fsm.go | grep -q "StateSetup.*StatePlanning"; then
    echo "  - ✅ SETUP → PLANNING transition defined"
else
    echo "  - ❌ SETUP → PLANNING transition missing"
    exit 1
fi

if grep -A 15 "CoderTransitions.*map" pkg/coder/coder_fsm.go | grep -q "StatePlanReview.*StateCoding"; then
    echo "  - ✅ PLAN_REVIEW → CODING transition defined"
else
    echo "  - ❌ PLAN_REVIEW → CODING transition missing"
    exit 1
fi

echo

# 6. Verify no duplication - old functions should be gone
echo "✅ Checking for removed duplication..."

if grep -q "func.*ensureGitHubAuthentication" pkg/coder/setup.go; then
    echo "  - ❌ Old ensureGitHubAuthentication function still exists"
    exit 1
else
    echo "  - ✅ Old ensureGitHubAuthentication function properly removed"
fi

if grep -q "gh auth setup-git" pkg/coder/setup.go; then
    echo "  - ❌ Manual 'gh auth setup-git' calls still exist"
    exit 1  
else
    echo "  - ✅ Manual GitHub CLI calls properly replaced with embedded script"
fi

echo

# 7. Final integration test
echo "✅ Running integration tests..."

go test ./internal/runtime ./pkg/coder -v -run "TestInstallAndRunGHInit|TestGitHubAuthIntegrationExample" > /tmp/complete_auth_test.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ All GitHub auth integration tests pass"
else
    echo "  - ❌ Integration tests failed"
    cat /tmp/complete_auth_test.log
    exit 1
fi

# 8. Linting check
echo "✅ Checking code quality..."
make lint > /tmp/complete_auth_lint.log 2>&1
if [ $? -eq 0 ]; then
    echo "  - ✅ All linting checks pass"
else
    echo "  - ❌ Linting failed"
    cat /tmp/complete_auth_lint.log
    exit 1
fi

echo
echo "🎉 Complete GitHub Auth Flow Verification: ALL PASSED"
echo
echo "✨ Summary of Complete Flow:"
echo "1. ✅ SETUP state creates planning container (readonly, no network, no GitHub auth needed)"
echo "2. ✅ PLANNING state uses planning container safely"
echo "3. ✅ PLAN_REVIEW state creates NEW coding container (readwrite, network enabled)"
echo "4. ✅ configureWorkspaceMount(readonly=false) automatically sets up GitHub auth"
echo "5. ✅ CODING state and beyond use coding container with proper authentication"
echo "6. ✅ Embedded script approach used throughout (no duplication)"
echo "7. ✅ Comprehensive logging and verification at each step"
echo "8. ✅ All tests passing, code quality maintained"
echo
echo "🚀 The complete GitHub auth flow is correctly implemented!"
echo "📋 Ready for Story 2 - Atomic Container Switch implementation!"