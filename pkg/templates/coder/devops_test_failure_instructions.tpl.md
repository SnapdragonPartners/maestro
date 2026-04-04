**IMPORTANT: Infrastructure tests are failing and must pass before proceeding.**

The test failure output is:

```
{{.Extra.Data}}
```

First, determine whether this failure is something you can fix:

- **If the failure is a Dockerfile issue, config error, or fixable infrastructure problem**: use container and shell tools (container_build, container_test, container_update, container_list, shell) to fix it, verify your fix, and call 'done' when infrastructure is working.
- **If the failure is an environment issue** (disk full, permissions, Docker daemon down, corrupt state, OOM) **or a missing prerequisite** (auth failure, missing API key, host unreachable, expired credentials): call `report_blocked` with the appropriate `failure_kind` (`environment` or `prerequisite`) and a clear explanation. Do not attempt to fix infrastructure problems you cannot control.

Do not simply explain what should be done - take action using the available tools.