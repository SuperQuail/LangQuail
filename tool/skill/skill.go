package skill

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

type ResourceKind string

const (
	ResourceKindReference ResourceKind = "reference"
	ResourceKindAsset     ResourceKind = "asset"
	ResourceKindScript    ResourceKind = "script"
)

type Skill struct {
	ID           string         `json:"id"`
	Description  string         `json:"description"`
	Instructions string         `json:"instructions,omitempty"`
	Root         string         `json:"root,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	UI           *UIMetadata    `json:"ui,omitempty"`
	Resources    []Resource     `json:"resources,omitempty"`
}

type Resource struct {
	SkillID string       `json:"skill_id"`
	Kind    ResourceKind `json:"kind"`
	Path    string       `json:"path"`
	AbsPath string       `json:"abs_path"`
	Size    int64        `json:"size"`
}

type UIMetadata struct {
	DisplayName      string         `json:"display_name,omitempty"`
	ShortDescription string         `json:"short_description,omitempty"`
	DefaultPrompt    string         `json:"default_prompt,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]Skill)}
}

func (r *Registry) Register(skill Skill) error {
	if r == nil {
		return errors.New("skill: nil registry")
	}
	if skill.ID == "" {
		return errors.New("skill: id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[skill.ID]; exists {
		return fmt.Errorf("skill: duplicate skill %q", skill.ID)
	}
	r.skills[skill.ID] = cloneSkill(skill)
	return nil
}

func (r *Registry) Get(id string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, exists := r.skills[id]
	if !exists {
		return Skill{}, false
	}
	return cloneSkill(skill), true
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.skills))
	for id := range r.skills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) List() []Skill {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.skills))
	for id := range r.skills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Skill, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneSkill(r.skills[id]))
	}
	return result
}

func (r *Registry) Resources(id string, kinds ...ResourceKind) ([]Resource, error) {
	if r == nil {
		return nil, errors.New("skill: nil registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, exists := r.skills[id]
	if !exists {
		return nil, fmt.Errorf("skill: skill %q is not registered", id)
	}
	if len(kinds) == 0 {
		return cloneResources(item.Resources), nil
	}
	allowed := make(map[ResourceKind]struct{}, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}
	result := make([]Resource, 0, len(item.Resources))
	for _, resource := range item.Resources {
		if _, ok := allowed[resource.Kind]; ok {
			result = append(result, resource)
		}
	}
	return cloneResources(result), nil
}

func cloneSkill(item Skill) Skill {
	item.Metadata = cloneAnyMap(item.Metadata)
	item.Resources = cloneResources(item.Resources)
	if item.UI != nil {
		ui := *item.UI
		ui.Metadata = cloneAnyMap(item.UI.Metadata)
		item.UI = &ui
	}
	return item
}

func cloneResources(resources []Resource) []Resource {
	if len(resources) == 0 {
		return nil
	}
	cloned := make([]Resource, len(resources))
	copy(cloned, resources)
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case map[any]any:
		cloned := make(map[string]any, len(typed))
		for key, value := range typed {
			cloned[fmt.Sprint(key)] = cloneAny(value)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneAny(item)
		}
		return cloned
	default:
		return value
	}
}
