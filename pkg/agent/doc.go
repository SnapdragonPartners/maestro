// Package agent provides the foundational abstractions for AI agents in the orchestrator.
//
// This package serves as the public API for agent functionality with the following structure:
//   - Core types and interfaces for agent drivers and state machines
//   - LLM client abstractions and message handling utilities
//   - Resilience utilities for circuit breaking, retries, and timeouts
//   - Configuration and validation helpers
//
// Internal implementation details are kept private under internal/ subdirectories.
package agent
