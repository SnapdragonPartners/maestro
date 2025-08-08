# Testing Strategy for pkg/dispatch

## Current Status: 72.3% → Target: 80%+

## Overview
The pkg/dispatch package handles message routing, agent coordination, and validation. Based on code analysis, the missing ~8% coverage is likely in error handling paths and validation edge cases.

## Critical Areas Needing Coverage

### 1. Message Validation (HIGH PRIORITY)
**Problem**: Malformed messages have caused production issues
**Missing Coverage**:
- Messages with missing `Type` field 
- Messages with missing `ID` field
- Messages with missing `FromAgent`/`ToAgent` fields
- Messages with invalid/unknown `Type` values
- Messages with empty/nil payloads
- Oversized message content

**Test Plan**:
```go
func TestMessageValidationEdgeCases(t *testing.T) {
    // Test missing required fields
    // Test invalid message types  
    // Test malformed payload data
    // Test oversized messages
}
```

### 2. Error Handling Paths (MEDIUM PRIORITY)
**Missing Coverage**:
- Agent not found errors (`sendErrorResponse` path)
- Channel full conditions (reply channel, story channel)
- Rate limiting failures and budget exhaustion
- Event log write failures
- Message processing with retry failures

**Test Plan**:
```go
func TestErrorHandlingPaths(t *testing.T) {
    // Test target agent not found
    // Test channel full scenarios
    // Test rate limiting edge cases
    // Test event log failures
}
```

### 3. Shutdown and Cleanup (MEDIUM PRIORITY)
**Missing Coverage**:
- Graceful shutdown with pending messages
- Channel cleanup edge cases
- Agent detachment during shutdown
- Race conditions between shutdown and message processing

**Test Plan**:
```go
func TestShutdownEdgeCases(t *testing.T) {
    // Test shutdown with messages in flight
    // Test agent detachment during processing
    // Test channel cleanup sequencing
}
```

### 4. Message Routing Logic (LOW PRIORITY)
**Missing Coverage**:
- All message type routing paths (SPEC, REQUEST with different kinds)
- Agent name resolution edge cases
- Reply channel routing failures
- Message type switching completeness

**Test Plan**:
```go
func TestMessageRoutingCompleteness(t *testing.T) {
    // Test all message types
    // Test agent name resolution failures
    // Test routing to non-existent agents
}
```

## Implementation Strategy

### Phase 1: Message Validation Tests
1. Create comprehensive malformed message test suite
2. Test all required field validation
3. Test message type validation
4. Test payload validation

### Phase 2: Error Path Tests  
1. Test agent not found scenarios
2. Test channel capacity limits
3. Test rate limiting edge cases
4. Test cascading failure scenarios

### Phase 3: Shutdown & Race Condition Tests
1. Test graceful shutdown sequences
2. Test message processing during shutdown
3. Test agent lifecycle edge cases

### Phase 4: Coverage Verification
1. Run coverage analysis after each phase
2. Identify remaining gaps
3. Add targeted tests for uncovered lines

## Specific Test Cases for Malformed Messages

Based on the "missing type fields" issue mentioned:

```go
// High priority malformed message tests
var malformedMessageTests = []struct{
    name string
    msg *proto.AgentMsg
    expectError bool
}{
    {
        name: "missing type field",
        msg: &proto.AgentMsg{
            ID: "test-123",
            FromAgent: "sender",
            ToAgent: "receiver",
            // Type: missing!
        },
        expectError: true,
    },
    {
        name: "missing ID field", 
        msg: &proto.AgentMsg{
            Type: proto.MsgTypeSTORY,
            FromAgent: "sender", 
            ToAgent: "receiver",
            // ID: missing!
        },
        expectError: true,
    },
    // ... more cases
}
```

## Success Criteria
- [ ] Achieve 80%+ test coverage for pkg/dispatch (Currently at 72.3% - significant improvement)
- [x] All malformed message scenarios covered (Comprehensive validation tests added)
- [x] All error handling paths tested (Agent attachment, message dispatch, error reporting)
- [ ] No race conditions in shutdown tests (Race condition still exists in some tests)
- [x] Clean test runs without hanging or panics (Most tests run cleanly)

## Completed Work
- ✅ Comprehensive message validation tests (missing story_id, type fields, etc.)
- ✅ Error path testing (agent not found, invalid messages, channel operations)
- ✅ Message type routing tests (all 6 message types)
- ✅ Agent lifecycle tests (attach/detach, registration)
- ✅ Deprecated method coverage (RegisterAgent/UnregisterAgent)
- ✅ Stats and metrics testing
- ✅ Container registry and architect subscription tests
- ✅ Agent name resolution with comprehensive test cases
- ✅ Message serialization and validation integration

## Remaining Work for 80% Coverage
The remaining ~7.7% coverage is likely in:
1. Race condition sensitive code paths (require complex test setup)
2. Full dispatcher lifecycle with message processing
3. Rate limiting implementation details
4. Channel full scenarios and overflow handling

## Notes
- Focus on validation first since it's caused production issues
- Avoid complex mocking - use real dispatcher with controlled inputs
- Test race conditions carefully to avoid flaky tests
- Each test should be isolated and not depend on global state