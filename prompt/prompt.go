package prompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"

	"gopkg.in/yaml.v3"
)

const (
	RoleSystem    = "system"
	RoleDeveloper = "developer"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type Segment struct {
	ID       string            `json:"id"`
	Role     string            `json:"role"`
	Source   string            `json:"source,omitempty"`
	Priority int               `json:"priority,omitempty"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Prompt struct {
	ID       string            `json:"id"`
	Segments []Segment         `json:"segments"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Draft struct {
	Segments []DraftSegment `json:"segments"`
}

type DraftSegment struct {
	ID       string            `json:"id,omitempty"`
	Role     string            `json:"role,omitempty"`
	Source   string            `json:"source,omitempty"`
	Priority int               `json:"priority,omitempty"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Registry struct {
	mu      sync.RWMutex
	root    string
	records map[string]record
	drafts  map[string]Draft
}

type Builder = Registry

type record struct {
	promptID string
	segment  Segment
	order    int
	path     string
}

type fileMeta struct {
	ID        string            `yaml:"id,omitempty"`
	SegmentID string            `yaml:"segment_id,omitempty"`
	Role      string            `yaml:"role,omitempty"`
	Source    string            `yaml:"source,omitempty"`
	Priority  int               `yaml:"priority,omitempty"`
	Order     int               `yaml:"order,omitempty"`
	Metadata  map[string]string `yaml:"metadata,omitempty"`
}

type loadOptions struct {
	recursive  bool
	extensions map[string]struct{}
}

type LoadOption func(*loadOptions)

func WithRecursive(value bool) LoadOption {
	return func(options *loadOptions) {
		options.recursive = value
	}
}

func WithExtensions(extensions ...string) LoadOption {
	return func(options *loadOptions) {
		options.extensions = make(map[string]struct{}, len(extensions))
		for _, extension := range extensions {
			normalized := strings.ToLower(extension)
			if normalized != "" && !strings.HasPrefix(normalized, ".") {
				normalized = "." + normalized
			}
			if normalized != "" {
				options.extensions[normalized] = struct{}{}
			}
		}
	}
}

func NewRegistry() *Registry {
	return &Registry{
		records: make(map[string]record),
		drafts:  make(map[string]Draft),
	}
}

func LoadDir(path string, opts ...LoadOption) (*Registry, error) {
	options := defaultLoadOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("prompt: %s is not a directory", path)
	}
	files, err := promptFiles(root, options)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("prompt: directory contains no prompt files")
	}
	registry := NewRegistry()
	registry.root = root
	for _, file := range files {
		record, err := loadFile(root, file)
		if err != nil {
			return nil, err
		}
		if _, exists := registry.records[record.promptID]; exists {
			return nil, fmt.Errorf("prompt: duplicate prompt id %q", record.promptID)
		}
		registry.records[record.promptID] = record
	}
	return registry, nil
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.records))
	for id := range r.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) Get(id string) (Prompt, error) {
	if r == nil {
		return Prompt{}, errors.New("prompt: nil registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, exists := r.records[id]
	if !exists {
		return Prompt{}, fmt.Errorf("prompt: prompt %q is not registered", id)
	}
	segment := cloneSegment(record.segment)
	if draft, ok := r.drafts[id]; ok {
		draftSegment, err := normalizeDraftSegment(record, draft)
		if err != nil {
			return Prompt{}, err
		}
		segment = draftSegment.toSegment(record.segment)
	}
	return Prompt{ID: id, Segments: []Segment{segment}}, nil
}

func (r *Registry) Render(ctx context.Context, id string, data map[string]any) (Prompt, error) {
	prompt, err := r.Get(id)
	if err != nil {
		return Prompt{}, err
	}
	rendered := Prompt{
		ID:       prompt.ID,
		Segments: make([]Segment, 0, len(prompt.Segments)),
		Metadata: cloneMetadata(prompt.Metadata),
	}
	for _, segment := range prompt.Segments {
		content, err := renderTemplate(ctx, segment.ID, segment.Content, data)
		if err != nil {
			return Prompt{}, err
		}
		segment.Content = content
		rendered.Segments = append(rendered.Segments, segment)
	}
	return rendered, nil
}

func (r *Registry) SaveDraft(id string, draft Draft) error {
	if r == nil {
		return errors.New("prompt: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.records[id]
	if !exists {
		return fmt.Errorf("prompt: prompt %q is not registered", id)
	}
	normalized, err := normalizeDraft(record, draft)
	if err != nil {
		return err
	}
	r.drafts[id] = normalized
	return nil
}

func (r *Registry) SaveDraftText(id string, content string) error {
	return r.SaveDraft(id, Draft{Segments: []DraftSegment{{Content: content}}})
}

func (r *Registry) CommitDraft(id string) error {
	if r == nil {
		return errors.New("prompt: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.records[id]
	if !exists {
		return fmt.Errorf("prompt: prompt %q is not registered", id)
	}
	draft, exists := r.drafts[id]
	if !exists {
		return fmt.Errorf("prompt: prompt %q has no draft", id)
	}
	draftSegment, err := normalizeDraftSegment(record, draft)
	if err != nil {
		return err
	}
	segment := draftSegment.toSegment(record.segment)
	content, err := marshalPromptFile(id, segment)
	if err != nil {
		return err
	}
	if err := os.WriteFile(record.path, content, 0o644); err != nil {
		return err
	}
	record.segment = segment
	r.records[id] = record
	delete(r.drafts, id)
	return nil
}

type registryContextKey struct{}

func WithRegistry(ctx context.Context, registry *Registry) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if registry == nil {
		return ctx
	}
	return context.WithValue(ctx, registryContextKey{}, registry)
}

func RegistryFromContext(ctx context.Context) (*Registry, bool) {
	if ctx == nil {
		return nil, false
	}
	registry, ok := ctx.Value(registryContextKey{}).(*Registry)
	return registry, ok && registry != nil
}

func defaultLoadOptions() loadOptions {
	return loadOptions{
		recursive: true,
		extensions: map[string]struct{}{
			".md":     {},
			".txt":    {},
			".prompt": {},
		},
	}
}

func promptFiles(root string, options loadOptions) ([]string, error) {
	var files []string
	if options.recursive {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path != root && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			if entry.Type().IsRegular() && shouldLoadPrompt(path, options) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				path := filepath.Join(root, entry.Name())
				if shouldLoadPrompt(path, options) {
					files = append(files, path)
				}
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

func shouldLoadPrompt(path string, options loadOptions) bool {
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(path))
	_, ok := options.extensions[extension]
	return ok
}

func loadFile(root string, path string) (record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return record{}, err
	}
	header, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return record{}, err
	}
	var meta fileMeta
	if header != "" {
		if err := yaml.Unmarshal([]byte(header), &meta); err != nil {
			return record{}, fmt.Errorf("prompt: parse frontmatter in %s: %w", path, err)
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return record{}, err
	}
	id := meta.ID
	if id == "" {
		id = defaultPromptID(rel)
	}
	segmentID := meta.SegmentID
	if segmentID == "" {
		segmentID = defaultPromptID(rel)
	}
	role := meta.Role
	if role == "" {
		role = RoleUser
	}
	if err := validateRole(role); err != nil {
		return record{}, fmt.Errorf("prompt: %s: %w", rel, err)
	}
	return record{
		promptID: id,
		order:    meta.Order,
		path:     path,
		segment: Segment{
			ID:       segmentID,
			Role:     role,
			Source:   defaultString(meta.Source, "template"),
			Priority: meta.Priority,
			Content:  body,
			Metadata: cloneMetadata(meta.Metadata),
		},
	}, nil
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
	return "", "", errors.New("prompt: unterminated frontmatter")
}

func defaultPromptID(path string) string {
	slash := filepath.ToSlash(path)
	withoutExt := strings.TrimSuffix(slash, filepath.Ext(slash))
	return strings.ReplaceAll(withoutExt, "/", ".")
}

func validateRole(role string) error {
	switch role {
	case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant:
		return nil
	default:
		return fmt.Errorf("invalid role %q", role)
	}
}

func renderTemplate(ctx context.Context, id string, content string, data map[string]any) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	tmpl, err := template.New(id).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", err
	}
	if data == nil {
		data = map[string]any{}
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", err
	}
	return rendered.String(), nil
}

func normalizeDraft(record record, draft Draft) (Draft, error) {
	segment, err := normalizeDraftSegment(record, draft)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Segments: []DraftSegment{segment}}, nil
}

func normalizeDraftSegment(record record, draft Draft) (DraftSegment, error) {
	if len(draft.Segments) != 1 {
		return DraftSegment{}, errors.New("prompt: draft must contain exactly one segment")
	}
	segment := draft.Segments[0]
	if segment.ID == "" {
		segment.ID = record.segment.ID
	}
	if segment.ID != record.segment.ID {
		return DraftSegment{}, fmt.Errorf("prompt: draft segment %q does not match %q", segment.ID, record.segment.ID)
	}
	if segment.Role == "" {
		segment.Role = record.segment.Role
	}
	if err := validateRole(segment.Role); err != nil {
		return DraftSegment{}, err
	}
	if segment.Source == "" {
		segment.Source = record.segment.Source
	}
	if segment.Metadata == nil {
		segment.Metadata = cloneMetadata(record.segment.Metadata)
	} else {
		segment.Metadata = cloneMetadata(segment.Metadata)
	}
	return segment, nil
}

func (s DraftSegment) toSegment(base Segment) Segment {
	if s.Priority == 0 {
		s.Priority = base.Priority
	}
	return Segment{
		ID:       s.ID,
		Role:     s.Role,
		Source:   s.Source,
		Priority: s.Priority,
		Content:  s.Content,
		Metadata: cloneMetadata(s.Metadata),
	}
}

func marshalPromptFile(promptID string, segment Segment) ([]byte, error) {
	header, err := yaml.Marshal(fileMeta{
		ID:        promptID,
		SegmentID: segment.ID,
		Role:      segment.Role,
		Source:    segment.Source,
		Priority:  segment.Priority,
		Metadata:  segment.Metadata,
	})
	if err != nil {
		return nil, err
	}
	content := "---\n" + string(header) + "---\n" + segment.Content
	return []byte(content), nil
}

func cloneSegment(segment Segment) Segment {
	segment.Metadata = cloneMetadata(segment.Metadata)
	return segment
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
