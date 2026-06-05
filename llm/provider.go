package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type Provider interface {
	Name() string
	Chat(context.Context, Request) (Response, error)
}

type ProviderSet struct {
	providers    map[string]Provider
	info         map[string]ProviderInfo
	models       map[string]map[string]ModelInfo
	defaultModel map[string]string
}

type ProviderInfo struct {
	ID           ProviderID        `json:"id"`
	Name         string            `json:"name,omitempty"`
	Env          []string          `json:"env,omitempty"`
	DefaultModel ModelID           `json:"default_model,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type ProviderRegistration struct {
	Provider Provider
	Info     ProviderInfo
	Models   []ModelInfo
}

type ProviderRegistrationOption func(*ProviderRegistration)

func RegisterProvider(provider Provider, opts ...ProviderRegistrationOption) ProviderRegistration {
	registration := ProviderRegistration{Provider: provider}
	if provider != nil {
		id := provider.Name()
		registration.Info = ProviderInfo{
			ID:   ProviderID(id),
			Name: id,
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&registration)
		}
	}
	if provider != nil && registration.Info.ID == "" {
		registration.Info.ID = ProviderID(provider.Name())
	}
	if registration.Info.Name == "" {
		registration.Info.Name = string(registration.Info.ID)
	}
	return registration
}

func WithProviderName(name string) ProviderRegistrationOption {
	return func(registration *ProviderRegistration) {
		registration.Info.Name = name
	}
}

func WithProviderEnv(names ...string) ProviderRegistrationOption {
	return func(registration *ProviderRegistration) {
		registration.Info.Env = append([]string(nil), names...)
	}
}

func WithProviderMetadata(metadata map[string]string) ProviderRegistrationOption {
	return func(registration *ProviderRegistration) {
		registration.Info.Metadata = metadata
	}
}

func WithModels(models ...ModelInfo) ProviderRegistrationOption {
	return func(registration *ProviderRegistration) {
		registration.Models = append([]ModelInfo(nil), models...)
	}
}

func WithDefaultModel(model string) ProviderRegistrationOption {
	return func(registration *ProviderRegistration) {
		registration.Info.DefaultModel = ModelID(model)
	}
}

func Providers(items ...Provider) ProviderSet {
	set := emptyProviderSet()
	for _, item := range items {
		if item == nil || item.Name() == "" {
			continue
		}
		name := item.Name()
		set.providers[name] = item
		set.info[name] = ProviderInfo{
			ID:   ProviderID(name),
			Name: name,
		}
	}
	return set
}

func NewProviderSet(registrations ...ProviderRegistration) (ProviderSet, error) {
	set := emptyProviderSet()
	for _, registration := range registrations {
		if registration.Provider == nil {
			return ProviderSet{}, errors.New("llm: provider is required")
		}
		name := registration.Provider.Name()
		if name == "" {
			return ProviderSet{}, errors.New("llm: provider name is required")
		}
		if _, exists := set.providers[name]; exists {
			return ProviderSet{}, fmt.Errorf("llm: provider %q is already registered", name)
		}

		info, err := normalizeProviderInfo(name, registration.Info)
		if err != nil {
			return ProviderSet{}, err
		}
		models := make(map[string]ModelInfo)
		for _, model := range registration.Models {
			normalized, err := normalizeModelInfo(name, model)
			if err != nil {
				return ProviderSet{}, err
			}
			modelID := string(normalized.ID)
			if _, exists := models[modelID]; exists {
				return ProviderSet{}, fmt.Errorf("llm: model %q is already registered for provider %q", modelID, name)
			}
			models[modelID] = normalized
		}
		if info.DefaultModel != "" {
			defaultModel := string(info.DefaultModel)
			if _, exists := models[defaultModel]; !exists {
				return ProviderSet{}, fmt.Errorf("llm: default model %q is not registered for provider %q", defaultModel, name)
			}
			set.defaultModel[name] = defaultModel
		}

		set.providers[name] = registration.Provider
		set.info[name] = info
		set.models[name] = models
	}
	return set, nil
}

func (s ProviderSet) Get(name string) (Provider, error) {
	if name == "" {
		return nil, errors.New("llm: provider name is required")
	}
	provider, exists := s.providers[name]
	if !exists {
		return nil, fmt.Errorf("llm: provider %q is not registered", name)
	}
	return provider, nil
}

func (s ProviderSet) Names() []string {
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s ProviderSet) ProviderInfo(name string) (ProviderInfo, error) {
	if name == "" {
		return ProviderInfo{}, errors.New("llm: provider name is required")
	}
	if _, exists := s.providers[name]; !exists {
		return ProviderInfo{}, fmt.Errorf("llm: provider %q is not registered", name)
	}
	return cloneProviderInfo(s.info[name]), nil
}

func (s ProviderSet) ProviderInfos() []ProviderInfo {
	names := s.Names()
	result := make([]ProviderInfo, 0, len(names))
	for _, name := range names {
		info, err := s.ProviderInfo(name)
		if err == nil {
			result = append(result, info)
		}
	}
	return result
}

func (s ProviderSet) Model(provider string, model ...string) (ModelInfo, error) {
	provider, modelID, err := modelLookup(provider, model...)
	if err != nil {
		return ModelInfo{}, err
	}
	if modelID == "" {
		return ModelInfo{}, errors.New("llm: model name is required")
	}
	if _, err := s.Get(provider); err != nil {
		return ModelInfo{}, err
	}
	models := s.models[provider]
	info, exists := models[modelID]
	if !exists {
		return ModelInfo{}, fmt.Errorf("llm: model %q is not registered for provider %q", modelID, provider)
	}
	return cloneModelInfo(info), nil
}

func (s ProviderSet) Models(provider string) ([]ModelInfo, error) {
	if _, err := s.Get(provider); err != nil {
		return nil, err
	}
	models := s.models[provider]
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneModelInfo(models[id]))
	}
	return result, nil
}

func (s ProviderSet) AllModels() []ModelInfo {
	providers := s.Names()
	result := make([]ModelInfo, 0)
	for _, provider := range providers {
		models, err := s.Models(provider)
		if err == nil {
			result = append(result, models...)
		}
	}
	return result
}

func (s ProviderSet) DefaultModel(provider string) (ModelInfo, error) {
	if _, err := s.Get(provider); err != nil {
		return ModelInfo{}, err
	}
	modelID := s.defaultModel[provider]
	if modelID == "" {
		return ModelInfo{}, fmt.Errorf("llm: provider %q has no default model", provider)
	}
	return s.Model(provider, modelID)
}

func emptyProviderSet() ProviderSet {
	return ProviderSet{
		providers:    make(map[string]Provider),
		info:         make(map[string]ProviderInfo),
		models:       make(map[string]map[string]ModelInfo),
		defaultModel: make(map[string]string),
	}
}

func normalizeProviderInfo(provider string, info ProviderInfo) (ProviderInfo, error) {
	if info.ID == "" {
		info.ID = ProviderID(provider)
	}
	if string(info.ID) != provider {
		return ProviderInfo{}, fmt.Errorf("llm: provider info id %q does not match provider %q", info.ID, provider)
	}
	if info.Name == "" {
		info.Name = provider
	}
	return cloneProviderInfo(info), nil
}

func normalizeModelInfo(provider string, model ModelInfo) (ModelInfo, error) {
	if model.ID == "" {
		return ModelInfo{}, errors.New("llm: model id is required")
	}
	if model.Provider == "" {
		model.Provider = ProviderID(provider)
	}
	if string(model.Provider) != provider {
		return ModelInfo{}, fmt.Errorf("llm: model %q provider %q does not match provider %q", model.ID, model.Provider, provider)
	}
	if model.Name == "" {
		model.Name = string(model.ID)
	}
	if model.Status == "" {
		model.Status = ModelStatusActive
	}
	return cloneModelInfo(model), nil
}

func cloneProviderInfo(info ProviderInfo) ProviderInfo {
	info.Env = append([]string(nil), info.Env...)
	info.Metadata = cloneStringMap(info.Metadata)
	return info
}

func modelLookup(provider string, model ...string) (string, string, error) {
	switch len(model) {
	case 0:
		ref, err := ParseModelRef(provider)
		if err != nil {
			return "", "", err
		}
		return string(ref.Provider), string(ref.Model), nil
	case 1:
		return provider, model[0], nil
	default:
		return "", "", errors.New("llm: model lookup accepts provider/model or provider, model")
	}
}
