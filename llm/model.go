package llm

import (
	"errors"
	"maps"
	"strings"
)

type ProviderID string

type ModelID string

type Model = ModelID

type ModelRef struct {
	Provider ProviderID `json:"provider"`
	Model    ModelID    `json:"model"`
}

func ParseModelRef(value string) (ModelRef, error) {
	value = strings.TrimSpace(value)
	index := strings.Index(value, "/")
	if index <= 0 || index == len(value)-1 {
		return ModelRef{}, errors.New("llm: model ref must use provider/model")
	}
	provider := strings.TrimSpace(value[:index])
	model := strings.TrimSpace(value[index+1:])
	if provider == "" || model == "" {
		return ModelRef{}, errors.New("llm: model ref must use provider/model")
	}
	return ModelRef{Provider: ProviderID(provider), Model: ModelID(model)}, nil
}

func (r ModelRef) String() string {
	return string(r.Provider) + "/" + string(r.Model)
}

type ModelStatus string

const (
	ModelStatusActive     ModelStatus = "active"
	ModelStatusBeta       ModelStatus = "beta"
	ModelStatusAlpha      ModelStatus = "alpha"
	ModelStatusDeprecated ModelStatus = "deprecated"
)

type ModelCapabilities struct {
	Tools  bool     `json:"tools"`
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type ModelLimits struct {
	Context int64 `json:"context,omitempty"`
	Input   int64 `json:"input,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

type ModelPricing struct {
	Currency   string  `json:"currency,omitempty"`
	PerTokens  int64   `json:"per_tokens,omitempty"`
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Context    int64   `json:"context,omitempty"`
}

type ModelInfo struct {
	ID           ModelID           `json:"id"`
	Provider     ProviderID        `json:"provider"`
	Name         string            `json:"name,omitempty"`
	Family       string            `json:"family,omitempty"`
	Capabilities ModelCapabilities `json:"capabilities"`
	Limits       ModelLimits       `json:"limits"`
	Pricing      []ModelPricing    `json:"pricing,omitempty"`
	Status       ModelStatus       `json:"status,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func cloneModelInfo(model ModelInfo) ModelInfo {
	model.Capabilities.Input = append([]string(nil), model.Capabilities.Input...)
	model.Capabilities.Output = append([]string(nil), model.Capabilities.Output...)
	model.Pricing = append([]ModelPricing(nil), model.Pricing...)
	model.Metadata = cloneStringMap(model.Metadata)
	return model
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return maps.Clone(values)
}
