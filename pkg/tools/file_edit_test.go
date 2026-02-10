package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	execpkg "orchestrator/pkg/exec"
)

// setupEditTest creates a temp dir with a file and returns the tool + cleanup.
func setupEditTest(t *testing.T, filename, content string) (*FileEditTool, string) {
	t.Helper()
	tmpDir := t.TempDir()

	if filename != "" {
		path := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
	}

	localExec := execpkg.NewLocalExec()
	tool := NewFileEditTool(localExec, tmpDir)
	return tool, tmpDir
}

func parseEditResponse(t *testing.T, result *ExecResult) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(result.Content), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	return resp
}

func TestFileEditTool_BasicEdit(t *testing.T) {
	tool, tmpDir := setupEditTest(t, "main.go", "package main\n\nfunc hello() string {\n\treturn \"hello\"\n}\n")

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "main.go",
		"old_string": "return \"hello\"",
		"new_string": "return \"world\"",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v: %v", resp["success"], resp["error"])
	}

	// Verify file was actually modified
	content, err := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if err != nil {
		t.Fatalf("failed to read modified file: %v", err)
	}
	expected := "package main\n\nfunc hello() string {\n\treturn \"world\"\n}\n"
	if string(content) != expected {
		t.Errorf("file content mismatch.\nexpected: %q\ngot:      %q", expected, string(content))
	}
}

func TestFileEditTool_OldStringNotFound(t *testing.T) {
	tool, _ := setupEditTest(t, "main.go", "package main\n\nfunc hello() {}\n")

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "main.go",
		"old_string": "this does not exist",
		"new_string": "replacement",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != false {
		t.Error("expected success=false for missing old_string")
	}
}

func TestFileEditTool_MultipleMatches(t *testing.T) {
	tool, _ := setupEditTest(t, "main.go", "foo\nbar\nfoo\nbaz\n")

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "main.go",
		"old_string": "foo",
		"new_string": "qux",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != false {
		t.Error("expected success=false for multiple matches")
	}
}

func TestFileEditTool_FileNotFound(t *testing.T) {
	tool, _ := setupEditTest(t, "", "") // No file created

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "missing.go",
		"old_string": "something",
		"new_string": "else",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != false {
		t.Error("expected success=false for missing file")
	}
}

func TestFileEditTool_DirectoryTraversal(t *testing.T) {
	tool, _ := setupEditTest(t, "", "")

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "../../etc/passwd",
		"old_string": "root",
		"new_string": "hacked",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != false {
		t.Error("expected success=false for directory traversal")
	}
}

func TestFileEditTool_Deletion(t *testing.T) {
	tool, tmpDir := setupEditTest(t, "main.go", "line1\ndelete_me\nline3\n")

	result, err := tool.Exec(context.Background(), map[string]any{
		"path":       "main.go",
		"old_string": "delete_me\n",
		"new_string": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := parseEditResponse(t, result)
	if resp["success"] != true {
		t.Errorf("expected success=true for deletion, got error: %v", resp["error"])
	}

	content, _ := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if string(content) != "line1\nline3\n" {
		t.Errorf("expected deletion, got: %q", string(content))
	}
}

func TestFileEditTool_EmptyOldString(t *testing.T) {
	tool, _ := setupEditTest(t, "main.go", "content")

	_, err := tool.Exec(context.Background(), map[string]any{
		"path":       "main.go",
		"old_string": "",
		"new_string": "something",
	})
	if err == nil {
		t.Error("expected error for empty old_string")
	}
}

func TestFileEditTool_MissingPath(t *testing.T) {
	tool, _ := setupEditTest(t, "", "")

	_, err := tool.Exec(context.Background(), map[string]any{
		"old_string": "foo",
		"new_string": "bar",
	})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestFileEditTool_Name(t *testing.T) {
	tool := NewFileEditTool(nil, "")
	if tool.Name() != ToolFileEdit {
		t.Errorf("expected name %q, got %q", ToolFileEdit, tool.Name())
	}
}

func TestFileEditTool_Definition(t *testing.T) {
	tool := NewFileEditTool(nil, "")
	def := tool.Definition()

	if def.Name != ToolFileEdit {
		t.Errorf("expected definition name %q, got %q", ToolFileEdit, def.Name)
	}
	if len(def.InputSchema.Required) != 3 {
		t.Errorf("expected 3 required params, got %d", len(def.InputSchema.Required))
	}
}
