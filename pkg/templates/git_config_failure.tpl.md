**Git Configuration Failed**

Could not configure git user identity:

```
{{.Error}}
```

**Action Required:**
You need to set git user.name and user.email manually using these exact commands:
- `git config --global user.name "{{.GitUserName}}"`
- `git config --global user.email "{{.GitUserEmail}}"`

This is required before making commits or pushes.