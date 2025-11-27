**IMPORTANT: Infrastructure tests are failing and must pass before proceeding.**

The test failure output is:

```
{{.Extra.Data}}
```

You must:
1. **Analyze the infrastructure failure** to understand what's wrong
2. **Use container and shell tools to fix the issues** (container_build, container_test, container_update, container_list, shell)
3. **Make concrete changes to resolve the infrastructure failures**
4. **Verify fixes using container tools** (prefer tools over CLI commands)
5. **Only call the 'done' tool when infrastructure is working correctly**

Do not simply explain what should be done - take action using the available tools to fix the failing infrastructure.