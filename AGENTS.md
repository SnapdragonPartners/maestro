# Codex Agents

## Review guidelines

Codex, when reviewing this repository:

- Prioritize **correctness, robustness, and maintainability** over cleverness or micro-optimizations.
- Assume this is a **single-user, locally run app** with a **moderate security posture**: we care about glaring issues and obviously unsafe practices, but we do not need enterprise-grade hardening.
- When in doubt, favor **simple, idiomatic Go**.

Only block the PR on **P0 and P1** issues:

- **P0** – Likely bugs, data corruption, crashes, or severe security issues.
- **P1** – Clear violations of these guidelines that will noticeably harm maintainability, clarity, or robustness.

Everything else should be treated as suggestions or questions.

---

### 1. Go style & modern features

- **Use modern Go constructs**:
  - Flag use of `interface{}`; prefer `any` in new code.
  - Prefer generics where they *simplify* code or eliminate duplication, not where they add unnecessary abstraction.
- Prefer **clear, idiomatic Go** over clever one-liners.
- Encourage **explicit error handling**:
  - Call out cases where errors are silently dropped or logged without context.
- Enforce **standard Go formatting and naming** where it materially improves clarity.

Treat non-idiomatic constructs that reduce readability as **P1** if they’re easy to fix.

---

### 2. SafeAssert vs. bare type assertions

We use a generics-based helper called **SafeAssert** to replace unsafe, brittle, or repeatedly duplicated type assertions.

Codex should:

- **Flag any bare type assertion** of the form:
  ```go
  v := x.(T)
  v, ok := x.(T)
  ```
- Recommend replacing it with the project’s **SafeAssert** pattern unless:
  - The assertion is performance-critical **and**
  - There is clear evidence it cannot fail (e.g., well-constrained generics type parameter, validated upstream).
- When SafeAssert improves clarity, error messaging, or robustness, prefer it even if the bare assertion includes `, ok`.

Treat unsafe or unjustified bare type assertions as **P1**.

---

### 3. Constants vs. literals

- Prefer **named constants** for:
  - Magic numbers.
  - Reused string literals (keys, paths, environment variables, API endpoints).
  - Timeouts, limits, well-known sizes.
- Flag repeated literals that should be constants.
- It’s okay to leave **obvious, single-use** literals inline (e.g., `len(x) == 0`).

Flag repeated or unclear literals as **P1**.

---

### 4. DRY, reuse, and robustness

- Watch for **duplicated logic** or near-duplicate code blocks.
- Prefer **well-tested shared helpers** over repeated custom implementations.
- Suggest extracting helpers or consolidating logic when reuse is clear and beneficial.
- If duplication is intentional (e.g., to avoid coupling), ask the author to confirm this in the PR.

Flag obvious duplication that would materially benefit from reuse as **P1**.

---

### 5. Abstraction level & architecture

- Push back on **unnecessary or overly layered abstractions**:
  - Interfaces with one implementation.
  - Thin wrapper layers that add no testability, clarity, or reuse.
- Prefer **simple, direct designs** over abstractions added “just in case”.
- Accept **purposeful abstractions** that meaningfully improve modularity or support multiple backends (e.g. LLM abstraction layer).

Flag clearly gratuitous abstraction as **P1**; treat borderline cases as questions.

---

### 6. Comments, TODOs, and deprecation

- Treat comments as part of the contract.
- Flag outdated or misleading comments.
- For `TODO`, `FIXME`, or deprecation notes:
  - Ask whether there is a **corresponding spec/plan/ticket**.
  - Ask for a reference to that item inside the comment (e.g. ticket or doc link).
  - Push back on TODOs that mask meaningful risks without tracking.

Treat critical-path TODOs without tracking as **P1**.

---

### 7. Dead code & cleanup

- Flag **orphaned or dead code** (unused functions, unreachable blocks, fields with no references).
- Ask for removal or clarification (e.g. feature gate, build tag usage).
- Allow code behind legitimate build tags or experiments when documented.

Treat clear dead code with no justification as **P1**.

---

### 8. Security posture (single-user, local)

Given this app is **single-user and locally run**, Codex should:

- Flag **glaring issues**:
  - Arbitrary command execution from untrusted input.
  - Unsanitized user-controlled paths passed to shell commands or filesystem ops.
  - Secrets committed to source.
- Be **lenient** on:
  - Running as root inside local Docker.
  - Debug logging in development configurations.
- Prefer **pragmatic mitigations** over heavy security refactors.

Treat only truly dangerous patterns as **P0**.

---

### 9. Testing expectations (unit + integration)

We do **not** enforce a strict test coverage threshold, but Codex should flag **obvious missing tests** where they would materially improve confidence or prevent regressions.

Codex should:

- Recommend adding **unit tests** for:
  - New logic with multiple branches or edge cases.
  - Reusable helpers or parsing/validation logic.
  - Behavior that previously caused bugs.
- Recommend adding **integration tests** (build tag: `integration`) for:
  - Cross-component workflows.
  - Realistic interactions with files, APIs, or external systems.
  - Cases where unit tests alone cannot provide coverage or fidelity.
- Avoid nitpicks when tests would provide little real value.

Missing tests for clearly complex or risk-prone logic should be treated as **P1**; otherwise treat as suggestions.

---

### 10. Clean code vs. expediency

- Encourage clean, readable, maintainable code.
- Push back when short-term hacks significantly increase long-term maintenance cost.
- Accept pragmatic shortcuts when clearly documented and tracked.

---

### 11. Tone & collaboration

- Use constructive, specific feedback.
- When guidelines conflict with established Go idioms or library conventions, prefer those idioms and call out the trade-offs rather than insisting.

