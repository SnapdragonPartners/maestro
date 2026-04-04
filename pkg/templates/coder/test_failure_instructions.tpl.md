**IMPORTANT: Tests are failing and must pass before proceeding.**

The test failure output is:

```
{{.Extra.Data}}
```

First, determine whether this failure is something you can fix:

- **If the failure is a code bug or a defective test**: fix the code or rewrite the test, verify with build/test/shell tools, and call 'done' when ready for the full test suite.
- **If the failure is an environment issue** (disk full, permissions, Docker daemon down, corrupt git state, OOM) **or a missing prerequisite** (auth failure, missing API key, host unreachable, expired credentials): call `report_blocked` with the appropriate `failure_kind` (`environment` or `prerequisite`) and a clear explanation. Do not attempt to fix infrastructure problems you cannot control.

Do not simply explain what should be done - take action using the available tools.