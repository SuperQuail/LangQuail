package prompt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superquail/langquail/prompt"
)

func TestLoadDirRenderDraftAndCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release", "plan.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`---
role: system
source: release
priority: 10
metadata:
  phase: planning
---
Plan {{.Change}}.`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry, err := prompt.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if ids := registry.IDs(); len(ids) != 1 || ids[0] != "release.plan" {
		t.Fatalf("IDs() = %#v", ids)
	}

	rendered, err := registry.Render(context.Background(), "release.plan", map[string]any{"Change": "database migration"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	segment := rendered.Segments[0]
	if segment.Role != prompt.RoleSystem || segment.Source != "release" || segment.Priority != 10 || segment.Content != "Plan database migration." {
		t.Fatalf("segment = %#v", segment)
	}
	if segment.Metadata["phase"] != "planning" {
		t.Fatalf("metadata = %#v", segment.Metadata)
	}

	if err := registry.SaveDraftText("release.plan", "Draft {{.Change}}."); err != nil {
		t.Fatalf("SaveDraftText() error = %v", err)
	}
	draft, err := registry.Render(context.Background(), "release.plan", map[string]any{"Change": "rollout"})
	if err != nil {
		t.Fatalf("Render() draft error = %v", err)
	}
	if draft.Segments[0].Content != "Draft rollout." {
		t.Fatalf("draft content = %q", draft.Segments[0].Content)
	}
	if err := registry.CommitDraft("release.plan"); err != nil {
		t.Fatalf("CommitDraft() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "Draft {{.Change}}.") {
		t.Fatalf("committed file = %q", string(raw))
	}
}

func TestPromptErrors(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		_, err := prompt.LoadDir(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "no prompt files") {
			t.Fatalf("LoadDir() error = %v", err)
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nid: same\n---\na"), 0o644); err != nil {
			t.Fatalf("WriteFile(a) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("---\nid: same\n---\nb"), 0o644); err != nil {
			t.Fatalf("WriteFile(b) error = %v", err)
		}
		_, err := prompt.LoadDir(dir)
		if err == nil || !strings.Contains(err.Error(), "duplicate prompt id") {
			t.Fatalf("LoadDir() error = %v", err)
		}
	})

	t.Run("invalid role", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("---\nrole: admin\n---\ncontent"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		_, err := prompt.LoadDir(dir)
		if err == nil || !strings.Contains(err.Error(), "invalid role") {
			t.Fatalf("LoadDir() error = %v", err)
		}
	})

	t.Run("missing render variable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "needs.md"), []byte("Hello {{.Name}}"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		registry, err := prompt.LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir() error = %v", err)
		}
		_, err = registry.Render(context.Background(), "needs", nil)
		if err == nil || !strings.Contains(err.Error(), "Name") {
			t.Fatalf("Render() error = %v", err)
		}
	})
}
