**Git Configuration Failed**

Could not configure git user identity:

```
{{.Extra.Data.Error}}
```

**Action Required:**
You need to set git user.name and user.email manually using these exact commands:
- `git config --global user.name "{{.Extra.Data.GitUserName}}"`
- `git config --global user.email "{{.Extra.Data.GitUserEmail}}"`

This is required before making commits or pushes.