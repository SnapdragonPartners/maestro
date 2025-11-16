# PM Bootstrap Integration - Implementation Stories

## Overview

This document organizes the PM bootstrap integration into logical implementation phases with clear evaluation breakpoints. Each phase builds upon the previous, with validation gates between phases.

## Phase 1: Foundation - Enable PM Without Repository

**Goal**: Allow PM to start and conduct interviews without a configured git repository.

### Story 1.1: Modify PM Workspace Creation
**Priority**: Critical
**Dependencies**: None
**Acceptance Criteria**:
- [ ] `EnsurePMWorkspace` creates minimal workspace when no git config exists
- [ ] PM starts successfully without repository
- [ ] PM can enter interview states without repo access
- [ ] Error handling for missing repo is graceful
**Files to Modify**:
- `pkg/workspace/pm.go`
- `pkg/pm/driver.go`

### Story 1.2: Add No-Repo State Tracking
**Priority**: High
**Dependencies**: Story 1.1
**Acceptance Criteria**:
- [ ] PM tracks whether it has repository access
- [ ] Tool availability adjusts based on repo presence
- [ ] State data includes `has_repository` flag
- [ ] Transitions work correctly in no-repo mode
**Files to Modify**:
- `pkg/pm/driver.go`
- `pkg/pm/states.go`

### Evaluation Checkpoint 1
- [ ] PM can start without repository
- [ ] Interview can begin without repo
- [ ] No crashes or panics in no-repo mode
- [ ] Clear state tracking of repo availability

---

## Phase 2: Bootstrap Detection

**Goal**: Detect missing bootstrap components and determine requirements.

### Story 2.1: Create Bootstrap Detector
**Priority**: Critical
**Dependencies**: Phase 1 complete
**Acceptance Criteria**:
- [ ] Detector identifies missing git repository
- [ ] Detector finds missing Dockerfile
- [ ] Detector checks for Makefile with required targets
- [ ] Detector identifies missing knowledge graph
- [ ] Platform detection from available files
**Files to Create**:
- `pkg/pm/bootstrap_detector.go`
- `pkg/pm/bootstrap_detector_test.go`

### Story 2.2: Integrate Detector into PM Workflow
**Priority**: High
**Dependencies**: Story 2.1
**Acceptance Criteria**:
- [ ] Detection runs early in interview
- [ ] Results stored in PM state
- [ ] Detection is expertise-aware
- [ ] Results influence question generation
**Files to Modify**:
- `pkg/pm/working.go`
- `pkg/pm/driver.go`

### Story 2.3: Platform Detection Logic
**Priority**: High
**Dependencies**: Story 2.1
**Acceptance Criteria**:
- [ ] Detect Go projects (go.mod, *.go files)
- [ ] Detect Python projects (requirements.txt, pyproject.toml, *.py)
- [ ] Detect Node projects (package.json, *.js/*.ts)
- [ ] Detect generic/unknown projects
- [ ] Confidence scoring for detection
**Files to Modify**:
- `pkg/pm/bootstrap_detector.go`

### Evaluation Checkpoint 2
- [ ] Bootstrap requirements accurately detected
- [ ] Platform detection works for common languages
- [ ] Detection results properly stored in state
- [ ] No false positives in detection

---

## Phase 3: Interview Integration

**Goal**: Seamlessly integrate bootstrap questions into the interview flow based on expertise.

### Story 3.1: Expertise-Based Question Generation
**Priority**: Critical
**Dependencies**: Phase 2 complete
**Acceptance Criteria**:
- [ ] NON_TECHNICAL: 3-4 basic questions only
- [ ] BASIC: 5-7 questions with confirmations
- [ ] EXPERT: 8-10 detailed technical questions
- [ ] Questions adapt to detected requirements
**Files to Modify**:
- `pkg/pm/working.go`
- `pkg/pm/interview.go` (may need creation)

### Story 3.2: Dynamic Question Injection
**Priority**: High
**Dependencies**: Story 3.1
**Acceptance Criteria**:
- [ ] Bootstrap questions appear naturally in flow
- [ ] Questions only asked when needed
- [ ] User responses properly captured
- [ ] Expertise level respected throughout
**Files to Modify**:
- `pkg/pm/working.go`
- `pkg/pm/await_user.go`

### Story 3.3: Repository URL Handling
**Priority**: High
**Dependencies**: Story 3.1
**Acceptance Criteria**:
- [ ] PM requests repo URL when missing
- [ ] Clear instructions for repo creation
- [ ] Handles both existing and non-existing repos
- [ ] Validates URL format
**Files to Modify**:
- `pkg/pm/working.go`

### Evaluation Checkpoint 3
- [ ] Interview flows smoothly with bootstrap questions
- [ ] Questions appropriate for expertise level
- [ ] All necessary bootstrap info collected
- [ ] User experience feels natural

---

## Phase 4: Specification Generation

**Goal**: Generate comprehensive specifications that include bootstrap requirements.

### Story 4.1: Bootstrap Spec Template
**Priority**: Critical
**Dependencies**: Phase 3 complete
**Acceptance Criteria**:
- [ ] Template includes knowledge graph initialization
- [ ] Git setup requirements detailed
- [ ] Container configuration specified
- [ ] Makefile requirements listed
- [ ] Platform-specific setup included
**Files to Create**:
- `pkg/templates/pm_bootstrap_template.go`

### Story 4.2: Knowledge Graph Default Content
**Priority**: Critical
**Dependencies**: Story 4.1
**Acceptance Criteria**:
- [ ] Default knowledge graph template created
- [ ] Basic patterns included (error handling, testing, docs)
- [ ] Format matches DOC_GRAPH.md specification
- [ ] Content is platform-agnostic
**Files to Create**:
- `pkg/pm/default_knowledge_graph.go`

### Story 4.3: Integrate Bootstrap into Spec Generation
**Priority**: High
**Dependencies**: Stories 4.1, 4.2
**Acceptance Criteria**:
- [ ] Bootstrap requirements appear first in spec
- [ ] Knowledge graph initialization is priority #1
- [ ] User requirements follow bootstrap
- [ ] Spec is complete and actionable for coders
**Files to Modify**:
- `pkg/pm/working.go`
- `pkg/pm/spec_generator.go` (may need creation)

### Story 4.4: Platform-Specific Bootstrap
**Priority**: Medium
**Dependencies**: Story 4.3
**Acceptance Criteria**:
- [ ] Go projects get Go-specific setup
- [ ] Python projects get Python-specific setup
- [ ] Node projects get Node-specific setup
- [ ] Generic projects get basic setup
**Files to Modify**:
- `pkg/templates/pm_bootstrap_template.go`

### Evaluation Checkpoint 4
- [ ] Generated specs include all bootstrap requirements
- [ ] Knowledge graph initialization is first
- [ ] Specs are complete and actionable
- [ ] Platform-specific details are correct

---

## Phase 5: Configuration and State Management

**Goal**: Update configuration management to support the new workflow.

### Story 5.1: Update PM Configuration
**Priority**: High
**Dependencies**: Phase 4 complete
**Acceptance Criteria**:
- [ ] Config supports PM without initial repo
- [ ] Bootstrap state tracked in config
- [ ] Expertise level configurable
- [ ] Session management works correctly
**Files to Modify**:
- `pkg/config/config.go`
- `pkg/pm/driver.go`

### Story 5.2: Git Configuration Updates
**Priority**: Medium
**Dependencies**: Story 5.1
**Acceptance Criteria**:
- [ ] Git config can be updated during interview
- [ ] Repo URL saved after collection
- [ ] Mirror configuration updated
- [ ] Target branch properly set
**Files to Modify**:
- `pkg/config/config.go`
- `pkg/pm/working.go`

### Evaluation Checkpoint 5
- [ ] Configuration properly updated during interview
- [ ] State persists across PM restarts
- [ ] Git configuration correctly saved
- [ ] No configuration conflicts

---

## Phase 6: Testing and Validation

**Goal**: Comprehensive testing of the integrated system.

### Story 6.1: Unit Tests for Bootstrap Detection
**Priority**: High
**Dependencies**: Phases 1-5 complete
**Acceptance Criteria**:
- [ ] Test detection with/without repo
- [ ] Test platform detection accuracy
- [ ] Test expertise-based logic
- [ ] Test edge cases and errors
**Files to Create**:
- `pkg/pm/bootstrap_detector_test.go`
- `pkg/pm/interview_test.go`

### Story 6.2: Integration Tests
**Priority**: High
**Dependencies**: Story 6.1
**Acceptance Criteria**:
- [ ] Test full interview flow without repo
- [ ] Test bootstrap spec generation
- [ ] Test each expertise level
- [ ] Test state transitions
**Files to Create**:
- `pkg/pm/integration_test.go`

### Story 6.3: End-to-End Testing
**Priority**: Critical
**Dependencies**: Story 6.2
**Acceptance Criteria**:
- [ ] Complete flow from no-repo to spec
- [ ] Verify coder can implement bootstrap
- [ ] Knowledge graph properly initialized
- [ ] Container strategy works correctly
**Files to Create**:
- `tests/e2e/pm_bootstrap_test.go`

### Evaluation Checkpoint 6
- [ ] All tests passing
- [ ] Edge cases handled
- [ ] No regressions in existing PM functionality
- [ ] Performance acceptable

---

## Phase 7: Migration and Cleanup

**Goal**: Remove legacy bootstrap and finalize the integration.

### Story 7.1: Remove CLI Bootstrap Flow
**Priority**: Medium
**Dependencies**: Phase 6 complete
**Acceptance Criteria**:
- [ ] Remove bootstrap command from CLI
- [ ] Remove interactive bootstrap code
- [ ] Clean up unused bootstrap packages
- [ ] Update help text and documentation
**Files to Remove/Modify**:
- `cmd/maestro/flows.go` (remove BootstrapFlow)
- `cmd/maestro/interactive_bootstrap.go` (remove)
- `pkg/bootstrap/*` (evaluate what to keep)

### Story 7.2: Update WebUI for PM Bootstrap
**Priority**: High
**Dependencies**: Story 7.1
**Acceptance Criteria**:
- [ ] WebUI only shows PM interview option
- [ ] No bootstrap button/flow
- [ ] Expertise selector in interview start
- [ ] Clear messaging about process
**Files to Modify**:
- WebUI components (specific files TBD)

### Story 7.3: Documentation Updates
**Priority**: Low
**Dependencies**: Story 7.2
**Acceptance Criteria**:
- [ ] README updated with new flow
- [ ] CLAUDE.md updated with bootstrap info
- [ ] Help text reflects changes
- [ ] Examples use new flow
**Files to Modify**:
- `README.md`
- `docs/CLAUDE.md`
- Various help texts

### Final Evaluation Checkpoint
- [ ] Legacy bootstrap completely removed
- [ ] New flow fully functional
- [ ] Documentation accurate
- [ ] No breaking changes for existing projects

---

## Implementation Notes

### Order of Implementation
1. **Phase 1-2**: Foundation and detection (can be done in parallel after 1.1)
2. **Phase 3-4**: Interview and spec generation (sequential)
3. **Phase 5**: Configuration (can parallel with testing)
4. **Phase 6**: Testing (after main implementation)
5. **Phase 7**: Cleanup (final phase)

### Risk Mitigation
- Each phase has clear evaluation criteria
- Early phases are low-risk foundation work
- Complex integration happens in middle phases
- Cleanup only after validation

### Rollback Strategy
- Git branch allows easy rollback
- Legacy bootstrap preserved until Phase 7
- Each phase independently testable
- No production impact (pre-release)

### Success Criteria
- PM can handle projects without initial repository
- Bootstrap requirements seamlessly integrated
- Knowledge graph always initialized
- Single workflow for all project initialization
- Clean removal of legacy code

## Estimated Story Points

| Phase | Story Points | Complexity |
|-------|-------------|------------|
| Phase 1 | 5 | Low |
| Phase 2 | 8 | Medium |
| Phase 3 | 13 | High |
| Phase 4 | 13 | High |
| Phase 5 | 5 | Low |
| Phase 6 | 8 | Medium |
| Phase 7 | 5 | Low |
| **Total** | **57** | **Medium-High** |

This represents a significant but manageable integration effort that fundamentally improves the user experience while maintaining system integrity.