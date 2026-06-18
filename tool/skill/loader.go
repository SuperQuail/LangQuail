package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadDir(path string) (Skill, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return Skill{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return Skill{}, err
	}
	if !info.IsDir() {
		return Skill{}, fmt.Errorf("skill: %s is not a directory", path)
	}
	realRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		realRoot = resolved
	}
	skillPath := filepath.Join(root, "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return Skill{}, err
	}
	header, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return Skill{}, fmt.Errorf("skill: parse frontmatter in %s: %w", skillPath, err)
	}
	var fields map[string]any
	if strings.TrimSpace(header) != "" {
		if err := yaml.Unmarshal([]byte(header), &fields); err != nil {
			return Skill{}, fmt.Errorf("skill: parse frontmatter in %s: %w", skillPath, err)
		}
	}
	id := stringField(fields, "name")
	description := stringField(fields, "description")
	if id == "" {
		return Skill{}, errors.New("skill: name is required")
	}
	if description == "" {
		return Skill{}, fmt.Errorf("skill: description is required for %q", id)
	}
	delete(fields, "name")
	delete(fields, "description")
	ui, err := loadUIMetadata(root)
	if err != nil {
		return Skill{}, err
	}
	resources, err := loadResources(root, realRoot, id)
	if err != nil {
		return Skill{}, err
	}
	return Skill{
		ID:           id,
		Description:  description,
		Instructions: body,
		Root:         root,
		Metadata:     cloneAnyMap(fields),
		UI:           ui,
		Resources:    resources,
	}, nil
}

func LoadRoots(paths ...string) (*Registry, error) {
	registry := NewRegistry()
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		loaded, err := loadRoot(path)
		if err != nil {
			return nil, err
		}
		for _, item := range loaded {
			if err := registry.Register(item); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}

func loadRoot(path string) ([]Skill, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skill: %s is not a directory", path)
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		item, err := LoadDir(root)
		if err != nil {
			return nil, err
		}
		return []Skill{item}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(child, "SKILL.md")); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		item, err := LoadDir(child)
		if err != nil {
			return nil, err
		}
		skills = append(skills, item)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].ID < skills[j].ID
	})
	return skills, nil
}

func loadUIMetadata(root string) (*UIMetadata, error) {
	path := filepath.Join(root, "agents", "openai.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var fields map[string]any
	if err := yaml.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("skill: parse UI metadata in %s: %w", path, err)
	}
	ui := &UIMetadata{
		DisplayName:      stringField(fields, "display_name"),
		ShortDescription: stringField(fields, "short_description"),
		DefaultPrompt:    stringField(fields, "default_prompt"),
	}
	delete(fields, "display_name")
	delete(fields, "short_description")
	delete(fields, "default_prompt")
	ui.Metadata = cloneAnyMap(fields)
	return ui, nil
}

func splitFrontmatter(content string) (string, string, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", normalized, nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", errors.New("unterminated frontmatter")
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
