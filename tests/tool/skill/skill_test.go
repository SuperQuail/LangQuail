package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superquail/langquail/tool/skill"
)

func TestLoadDirParsesCodexSkillAndResources(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "frontend-design", "Build polished UIs")
	writeFile(t, filepath.Join(root, "agents", "openai.yaml"), "display_name: Frontend Design\nshort_description: Build UIs\ndefault_prompt: Make it polished\nicon: sparkle\n")
	writeFile(t, filepath.Join(root, "references", "style.md"), "# Style\n")
	writeFile(t, filepath.Join(root, "assets", "template.txt"), "asset")
	writeFile(t, filepath.Join(root, "scripts", "build.js"), "console.log('ok')\n")

	loaded, err := skill.LoadDir(root)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if loaded.ID != "frontend-design" || loaded.Description != "Build polished UIs" {
		t.Fatalf("skill identity = %#v", loaded)
	}
	if !strings.Contains(loaded.Instructions, "Use this skill carefully.") {
		t.Fatalf("instructions = %q", loaded.Instructions)
	}
	if loaded.Metadata["metadata"] == nil {
		t.Fatalf("metadata = %#v", loaded.Metadata)
	}
	if loaded.UI == nil || loaded.UI.DisplayName != "Frontend Design" || loaded.UI.Metadata["icon"] != "sparkle" {
		t.Fatalf("ui = %#v", loaded.UI)
	}
	counts := resourceCounts(loaded.Resources)
	if counts[skill.ResourceKindReference] != 2 || counts[skill.ResourceKindAsset] != 1 || counts[skill.ResourceKindScript] != 1 {
		t.Fatalf("resource counts = %#v resources=%#v", counts, loaded.Resources)
	}
	for _, resource := range loaded.Resources {
		if resource.SkillID != loaded.ID || resource.Path == "" || resource.AbsPath == "" || resource.Size == 0 {
			t.Fatalf("resource = %#v", resource)
		}
		if strings.Contains(resource.Path, `\`) {
			t.Fatalf("resource path = %q, want slash path", resource.Path)
		}
	}
}

func TestLoadDirValidationErrors(t *testing.T) {
	t.Run("missing skill file", func(t *testing.T) {
		_, err := skill.LoadDir(t.TempDir())
		if err == nil {
			t.Fatal("LoadDir() error is nil")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "SKILL.md"), "---\ndescription: Missing name\n---\nBody")
		_, err := skill.LoadDir(root)
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("LoadDir() error = %v", err)
		}
	})

	t.Run("missing description", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: missing-description\n---\nBody")
		_, err := skill.LoadDir(root)
		if err == nil || !strings.Contains(err.Error(), "description is required") {
			t.Fatalf("LoadDir() error = %v", err)
		}
	})
}

func TestRegistryCopiesValuesAndFiltersResources(t *testing.T) {
	loaded, err := skill.LoadDir(writeSkill(t, t.TempDir(), "writer", "Write docs"))
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	registry := skill.NewRegistry()
	if err := registry.Register(loaded); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(loaded); err == nil {
		t.Fatal("Register(duplicate) error is nil")
	}
	got, exists := registry.Get("writer")
	if !exists {
		t.Fatal("Get() missing")
	}
	got.Description = "mutated"
	got.Metadata["metadata"] = "mutated"
	got.Resources[0].Path = "mutated"

	again, exists := registry.Get("writer")
	if !exists {
		t.Fatal("Get() missing after mutation")
	}
	if again.Description != "Write docs" || again.Metadata["metadata"] == "mutated" || again.Resources[0].Path == "mutated" {
		t.Fatalf("registry value was mutated: %#v", again)
	}
	if ids := registry.IDs(); len(ids) != 1 || ids[0] != "writer" {
		t.Fatalf("IDs() = %#v", ids)
	}
	resources, err := registry.Resources("writer", skill.ResourceKindReference)
	if err != nil {
		t.Fatalf("Resources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].Kind != skill.ResourceKindReference {
		t.Fatalf("filtered resources = %#v", resources)
	}
	if _, err := registry.Resources("missing"); err == nil {
		t.Fatal("Resources(missing) error is nil")
	}
}

func TestLoadRootsScansSkillRootsAndRejectsDuplicates(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "Alpha skill")
	writeSkill(t, root, "beta", "Beta skill")
	registry, err := skill.LoadRoots(root)
	if err != nil {
		t.Fatalf("LoadRoots() error = %v", err)
	}
	if ids := registry.IDs(); len(ids) != 2 || ids[0] != "alpha" || ids[1] != "beta" {
		t.Fatalf("IDs() = %#v", ids)
	}

	dupeRoot := t.TempDir()
	writeSkill(t, dupeRoot, "same", "Same skill")
	writeSkill(t, dupeRoot, "same-copy", "Same skill")
	replaceSkillName(t, filepath.Join(dupeRoot, "same-copy", "SKILL.md"), "same")
	if _, err := skill.LoadRoots(dupeRoot); err == nil {
		t.Fatal("LoadRoots(duplicate) error is nil")
	}
}

func TestLoadDirRejectsSymlinkEscape(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "escape", "Escape test")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "outside")
	link := filepath.Join(root, "references", "outside.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	_, err := skill.LoadDir(root)
	if err == nil || !strings.Contains(err.Error(), "escapes skill root") {
		t.Fatalf("LoadDir() error = %v", err)
	}
}

func writeSkill(t *testing.T, parent string, name string, description string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: "+name+"\ndescription: "+description+"\nmetadata:\n  owner: docs\n---\nUse this skill carefully.\n")
	writeFile(t, filepath.Join(root, "references", "guide.md"), "# Guide\n")
	return root
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func replaceSkillName(t *testing.T, path string, name string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	content := string(raw)
	start := strings.Index(content, "name: ")
	if start < 0 {
		t.Fatalf("name field not found in %s", path)
	}
	end := strings.Index(content[start:], "\n")
	if end < 0 {
		t.Fatalf("name field line end not found in %s", path)
	}
	content = content[:start] + "name: " + name + content[start+end:]
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func resourceCounts(resources []skill.Resource) map[skill.ResourceKind]int {
	counts := make(map[skill.ResourceKind]int)
	for _, resource := range resources {
		counts[resource.Kind]++
	}
	return counts
}
