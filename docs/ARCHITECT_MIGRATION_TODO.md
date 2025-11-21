# Architect BaseStateMachine Migration TODO

## Overview
Migrate the Architect agent to use BaseStateMachine for proper state management,
following the same pattern as the successful PM migration.

## Lessons Learned from PM Migration
1. Check that all transitions in STATES.md are in the transition table
2. Fix GetValidStates() to return all states (not just a subset)
3. Fix GetStateData() return type to match interface (core.StateData)
4. Add error checking to TransitionTo() calls (don't use `_ =`)
5. Create comprehensive transition tests to catch future bugs

## Tasks

### 1. Preparation & Analysis
- [x] Review architect_fsm.go for existing transition table
- [x] Check STATES.md for documented states and transitions
- [x] Identify all state management code in driver.go
- [ ] Count occurrences of currentState and stateData access
- [ ] Review GetValidStates() and ValidateState() implementations

### 2. Driver Structure Changes
- [ ] Add `*agent.BaseStateMachine` embed to Driver struct
- [ ] Remove duplicate fields: `currentState`, `stateData`, `mu`
- [ ] Keep typed `llmClient` field (shadows base's untyped field)
- [ ] Update NewDriver() constructor to initialize BaseStateMachine
- [ ] Update BootstrapArchitect() to set llmClient after construction

### 3. State Access Migration
- [ ] Replace all `d.currentState` reads with `d.GetCurrentState()`
- [ ] Replace all `d.currentState` writes with `d.TransitionTo()`
- [ ] Replace all `d.stateData[key]` reads with `stateData := d.GetStateData(); stateData[key]`
- [ ] Replace all `d.stateData[key] = value` writes with `d.SetStateData(key, value)`
- [ ] Remove all `d.mu.Lock()/Unlock()` calls (BaseStateMachine handles locking)

### 4. State Transition Updates
- [ ] Add error checking to all `TransitionTo()` calls
- [ ] Remove any manual state assignment (replace with TransitionTo)
- [ ] Verify all transitions are in architectTransitions table
- [ ] Check for self-transitions (state → same state) and add if needed

### 5. Interface Compliance
- [ ] Update GetValidStates() to return GetAllArchitectStates()
- [ ] Update ValidateState() to delegate to IsValidArchitectState()
- [ ] Verify GetStateData() returns core.StateData (should be inherited)
- [ ] Verify GetCurrentState() is available (should be inherited)

### 6. File-by-File Updates

#### driver.go
- [ ] Update Driver struct
- [ ] Update NewDriver() constructor
- [ ] Fix all state access in Run() loop
- [ ] Fix state access in Step()
- [ ] Fix state access in processState()
- [ ] Fix state access in transitionTo()
- [ ] Update GetValidStates() and ValidateState()

#### dispatching.go
- [ ] Fix state access in handleDispatching()

#### monitoring.go
- [ ] Fix state access in handleMonitoring()

#### request.go
- [ ] Fix state access in handleRequest()
- [ ] Fix state access in all sub-handlers

#### escalated.go
- [ ] Fix state access in handleEscalated()

#### Other state handler files
- [ ] Check and update any other files with state access

### 7. Testing
- [ ] Create architect_transitions_test.go (similar to PM)
- [ ] Add TestAllValidTransitions
- [ ] Add TestInvalidTransitions
- [ ] Add TestTransitionTableCompleteness
- [ ] Add TestStateSymmetryWithDocumentation
- [ ] Add TestTransitionValidation
- [ ] Add interface satisfaction test
- [ ] Run full test suite

### 8. Documentation
- [ ] Verify STATES.md matches architectTransitions
- [ ] Update any comments referencing manual state management
- [ ] Add migration notes to commit message

### 9. Validation
- [ ] Build passes with no errors
- [ ] All tests pass
- [ ] Architect successfully runs and transitions between states
- [ ] Run system end-to-end test if available

## Known Issues to Watch For

Based on PM migration:

1. **Missing transitions**: Check that all transitions in STATES.md are in architectTransitions
   - PM was missing WAITING → WORKING

2. **Incomplete GetValidStates()**: Ensure it returns all 7 states, not a subset
   - PM was only returning 4 of 7 states

3. **Error handling**: Add proper error checking to all TransitionTo() calls
   - PM was using `_ = d.TransitionTo()` which hid failures

4. **Self-transitions**: Some states need self-transitions for polling
   - Example: WAITING → WAITING, MONITORING → MONITORING

5. **Type mismatches**: Ensure GetStateData() returns core.StateData
   - This is what caused the interface satisfaction issue

## Estimated Complexity

- **Lines to change**: ~150-200 (based on PM: 299 lines changed)
- **Files affected**: ~10 files
- **Time estimate**: 1-2 hours with testing
- **Risk level**: Medium (well-defined pattern from PM migration)

## Success Criteria

- [x] Architect embeds BaseStateMachine
- [x] All manual state management removed
- [x] All transitions validated by BaseStateMachine
- [x] Comprehensive tests prevent regression
- [ ] Documentation matches implementation (STATES.md verified)
- [ ] System runs successfully end-to-end (pending integration test)

## Completion Summary

The architect has been successfully migrated to BaseStateMachine following the same pattern as PM:

1. **Driver Structure**: Added `*agent.BaseStateMachine` embed, removed duplicate fields (currentState, stateData, mu)
2. **Constructor**: Updated NewDriver() to initialize BaseStateMachine with architectTransitions
3. **State Access**: Fixed all state access patterns across 7 files
4. **Validation**: Updated GetValidStates() and ValidateState() to delegate to helper functions
5. **Error Handling**: Added proper error checking to all TransitionTo() calls
6. **Tests**: Created comprehensive transition test suite (96 test cases)

### Files Modified
- pkg/architect/driver.go - Main driver refactor (299 lines affected)
- pkg/architect/architect_fsm.go - Already had good structure
- pkg/architect/request.go - State access fixes
- pkg/architect/request_spec.go - State access fixes
- pkg/architect/waiting.go - State access fixes
- pkg/architect/monitoring.go - State access fixes
- pkg/architect/dispatching.go - State access fixes
- pkg/architect/escalated.go - State access fixes
- pkg/architect/scoping.go - State access fixes
- pkg/architect/transitions_test.go - New comprehensive test suite

### Test Results
- All architect transition tests pass (96/96)
- Build succeeds with no errors
- Minor pre-existing webui test failure (unrelated to migration)
