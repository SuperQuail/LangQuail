package llm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
)

func TestProviderSetRegistersProviderModelsAndDefault(t *testing.T) {
	providers, err := llm.NewProviderSet(llm.RegisterProvider(
		registryProvider{name: "openai"},
		llm.WithProviderName("OpenAI"),
		llm.WithProviderEnv("OPENAI_API_KEY"),
		llm.WithProviderMetadata(map[string]string{"kind": "cloud"}),
		llm.WithModels(
			llm.ModelInfo{
				ID:   "gpt-4.1",
				Name: "GPT-4.1",
				Capabilities: llm.ModelCapabilities{
					Tools:  true,
					Input:  []string{"text", "image/*"},
					Output: []string{"text"},
				},
				Limits: llm.ModelLimits{Context: 1_000_000, Output: 32_768},
				Pricing: []llm.ModelPricing{{
					Currency:  "USD",
					PerTokens: 1_000_000,
					Input:     2,
					Output:    8,
				}},
				Metadata: map[string]string{"tier": "frontier"},
			},
			llm.ModelInfo{
				ID:   "openai/gpt-4o-mini",
				Name: "GPT-4o mini via OpenRouter-style ID",
			},
		),
		llm.WithDefaultModel("gpt-4.1"),
	))
	if err != nil {
		t.Fatalf("NewProviderSet() error = %v", err)
	}

	info, err := providers.ProviderInfo("openai")
	if err != nil {
		t.Fatalf("ProviderInfo() error = %v", err)
	}
	if info.ID != "openai" || info.Name != "OpenAI" || info.DefaultModel != "gpt-4.1" {
		t.Fatalf("ProviderInfo() = %#v", info)
	}
	if len(info.Env) != 1 || info.Env[0] != "OPENAI_API_KEY" || info.Metadata["kind"] != "cloud" {
		t.Fatalf("ProviderInfo metadata = %#v", info)
	}

	model, err := providers.Model("openai", "gpt-4.1")
	if err != nil {
		t.Fatalf("Model() error = %v", err)
	}
	if model.Provider != "openai" || model.Status != llm.ModelStatusActive || !model.Capabilities.Tools {
		t.Fatalf("Model() = %#v", model)
	}
	if model.Limits.Context != 1_000_000 || model.Pricing[0].PerTokens != 1_000_000 {
		t.Fatalf("Model limits/pricing = %#v", model)
	}
	refModel, err := providers.Model("openai/gpt-4.1")
	if err != nil {
		t.Fatalf("Model(provider/model) error = %v", err)
	}
	if refModel.ID != model.ID || refModel.Provider != model.Provider {
		t.Fatalf("Model(provider/model) = %#v, want %#v", refModel, model)
	}
	slashModel, err := providers.Model("openai/openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("Model(provider/slashed-model) error = %v", err)
	}
	if slashModel.ID != "openai/gpt-4o-mini" {
		t.Fatalf("Model(provider/slashed-model) = %#v", slashModel)
	}

	defaultModel, err := providers.DefaultModel("openai")
	if err != nil {
		t.Fatalf("DefaultModel() error = %v", err)
	}
	if defaultModel.ID != "gpt-4.1" {
		t.Fatalf("DefaultModel() = %#v", defaultModel)
	}

	models, err := providers.Models("openai")
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("Models() = %#v", models)
	}
	if all := providers.AllModels(); len(all) != 2 {
		t.Fatalf("AllModels() = %#v", all)
	}
	if infos := providers.ProviderInfos(); len(infos) != 1 || infos[0].ID != "openai" {
		t.Fatalf("ProviderInfos() = %#v", infos)
	}
}

func TestProviderSetQueryResultsAreCloned(t *testing.T) {
	providers, err := llm.NewProviderSet(llm.RegisterProvider(
		registryProvider{name: "openai"},
		llm.WithProviderEnv("OPENAI_API_KEY"),
		llm.WithProviderMetadata(map[string]string{"kind": "cloud"}),
		llm.WithModels(llm.ModelInfo{
			ID: "gpt-4.1",
			Capabilities: llm.ModelCapabilities{
				Input:  []string{"text"},
				Output: []string{"text"},
			},
			Pricing:  []llm.ModelPricing{{Currency: "USD", Input: 1}},
			Metadata: map[string]string{"tier": "frontier"},
		}),
		llm.WithDefaultModel("gpt-4.1"),
	))
	if err != nil {
		t.Fatalf("NewProviderSet() error = %v", err)
	}

	info, err := providers.ProviderInfo("openai")
	if err != nil {
		t.Fatalf("ProviderInfo() error = %v", err)
	}
	info.Env[0] = "MUTATED"
	info.Metadata["kind"] = "mutated"
	againInfo, err := providers.ProviderInfo("openai")
	if err != nil {
		t.Fatalf("ProviderInfo() again error = %v", err)
	}
	if againInfo.Env[0] != "OPENAI_API_KEY" || againInfo.Metadata["kind"] != "cloud" {
		t.Fatalf("ProviderInfo() was externally mutated: %#v", againInfo)
	}

	model, err := providers.Model("openai", "gpt-4.1")
	if err != nil {
		t.Fatalf("Model() error = %v", err)
	}
	model.Capabilities.Input[0] = "audio"
	model.Pricing[0].Currency = "EUR"
	model.Metadata["tier"] = "mutated"
	againModel, err := providers.Model("openai", "gpt-4.1")
	if err != nil {
		t.Fatalf("Model() again error = %v", err)
	}
	if againModel.Capabilities.Input[0] != "text" || againModel.Pricing[0].Currency != "USD" || againModel.Metadata["tier"] != "frontier" {
		t.Fatalf("Model() was externally mutated: %#v", againModel)
	}
}

func TestParseModelRefAllowsSlashesInModelID(t *testing.T) {
	ref, err := llm.ParseModelRef("openrouter/openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("ParseModelRef() error = %v", err)
	}
	if ref.Provider != "openrouter" || ref.Model != "openai/gpt-4o-mini" {
		t.Fatalf("ParseModelRef() = %#v", ref)
	}
	if ref.String() != "openrouter/openai/gpt-4o-mini" {
		t.Fatalf("String() = %q", ref.String())
	}

	for _, value := range []string{"", "openai", "/gpt-4.1", "openai/", " /gpt-4.1", "openai/   "} {
		if _, err := llm.ParseModelRef(value); err == nil {
			t.Fatalf("ParseModelRef(%q) error = nil", value)
		}
	}
}

func TestNewProviderSetRejectsInvalidRegistrations(t *testing.T) {
	tests := []struct {
		name          string
		registrations []llm.ProviderRegistration
		want          string
	}{
		{
			name:          "nil provider",
			registrations: []llm.ProviderRegistration{{}},
			want:          "provider is required",
		},
		{
			name:          "empty provider name",
			registrations: []llm.ProviderRegistration{llm.RegisterProvider(registryProvider{})},
			want:          "provider name",
		},
		{
			name: "duplicate provider",
			registrations: []llm.ProviderRegistration{
				llm.RegisterProvider(registryProvider{name: "openai"}),
				llm.RegisterProvider(registryProvider{name: "openai"}),
			},
			want: "already registered",
		},
		{
			name: "provider info mismatch",
			registrations: []llm.ProviderRegistration{{
				Provider: registryProvider{name: "openai"},
				Info:     llm.ProviderInfo{ID: "anthropic"},
			}},
			want: "does not match provider",
		},
		{
			name: "empty model id",
			registrations: []llm.ProviderRegistration{llm.RegisterProvider(
				registryProvider{name: "openai"},
				llm.WithModels(llm.ModelInfo{}),
			)},
			want: "model id",
		},
		{
			name: "model provider mismatch",
			registrations: []llm.ProviderRegistration{llm.RegisterProvider(
				registryProvider{name: "openai"},
				llm.WithModels(llm.ModelInfo{ID: "claude", Provider: "anthropic"}),
			)},
			want: "does not match provider",
		},
		{
			name: "duplicate model",
			registrations: []llm.ProviderRegistration{llm.RegisterProvider(
				registryProvider{name: "openai"},
				llm.WithModels(llm.ModelInfo{ID: "gpt-4.1"}, llm.ModelInfo{ID: "gpt-4.1"}),
			)},
			want: "model \"gpt-4.1\" is already registered",
		},
		{
			name: "missing default model",
			registrations: []llm.ProviderRegistration{llm.RegisterProvider(
				registryProvider{name: "openai"},
				llm.WithModels(llm.ModelInfo{ID: "gpt-4.1"}),
				llm.WithDefaultModel("missing"),
			)},
			want: "default model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := llm.NewProviderSet(tt.registrations...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewProviderSet() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProviderSetQueryErrorsAndRegistrationDefaults(t *testing.T) {
	providers, err := llm.NewProviderSet(llm.ProviderRegistration{
		Provider: registryProvider{name: "openai"},
		Models:   []llm.ModelInfo{{ID: "gpt-4.1"}},
	})
	if err != nil {
		t.Fatalf("NewProviderSet() error = %v", err)
	}
	info, err := providers.ProviderInfo("openai")
	if err != nil {
		t.Fatalf("ProviderInfo() error = %v", err)
	}
	if info.ID != "openai" || info.Name != "openai" {
		t.Fatalf("ProviderInfo() = %#v", info)
	}
	model, err := providers.Model("openai", "gpt-4.1")
	if err != nil {
		t.Fatalf("Model() error = %v", err)
	}
	if model.Provider != "openai" || model.Name != "gpt-4.1" || model.Status != llm.ModelStatusActive {
		t.Fatalf("Model() = %#v", model)
	}

	if _, err := providers.Get(""); err == nil || !strings.Contains(err.Error(), "provider name") {
		t.Fatalf("Get(empty) error = %v", err)
	}
	if _, err := providers.ProviderInfo(""); err == nil || !strings.Contains(err.Error(), "provider name") {
		t.Fatalf("ProviderInfo(empty) error = %v", err)
	}
	if _, err := providers.ProviderInfo("missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("ProviderInfo(missing) error = %v", err)
	}
	if _, err := providers.Model("openai", ""); err == nil || !strings.Contains(err.Error(), "model name") {
		t.Fatalf("Model(empty) error = %v", err)
	}
	if _, err := providers.Model("missing", "gpt-4.1"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Model(missing provider) error = %v", err)
	}
	if _, err := providers.Model("openai"); err == nil || !strings.Contains(err.Error(), "provider/model") {
		t.Fatalf("Model(invalid ref) error = %v", err)
	}
	if _, err := providers.Model("openai", "gpt-4.1", "extra"); err == nil || !strings.Contains(err.Error(), "accepts provider/model") {
		t.Fatalf("Model(extra args) error = %v", err)
	}
	if _, err := providers.Models("missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Models(missing provider) error = %v", err)
	}
	if _, err := providers.DefaultModel("missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("DefaultModel(missing provider) error = %v", err)
	}
}

func TestLegacyProvidersRemainExecutionOnly(t *testing.T) {
	providers := llm.Providers(registryProvider{name: "legacy"}, registryProvider{})
	names := providers.Names()
	if len(names) != 1 || names[0] != "legacy" {
		t.Fatalf("Names() = %#v", names)
	}
	if models, err := providers.Models("legacy"); err != nil || len(models) != 0 {
		t.Fatalf("Models() = %#v, %v", models, err)
	}
	if _, err := providers.DefaultModel("legacy"); err == nil || !strings.Contains(err.Error(), "no default model") {
		t.Fatalf("DefaultModel() error = %v", err)
	}
	if _, err := providers.Model("legacy/any-model/with/slash"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Model(provider/model) error = %v", err)
	}
	provider, err := providers.Get("legacy")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	response, err := provider.Chat(context.Background(), llm.Request{Model: "any-model"})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Model != "any-model" {
		t.Fatalf("Chat() response = %#v", response)
	}
}

type registryProvider struct {
	name string
}

func (p registryProvider) Name() string {
	return p.name
}

func (p registryProvider) Chat(_ context.Context, request llm.Request) (llm.Response, error) {
	return llm.Response{
		Model:   request.Model,
		Message: llm.Assistant("ok"),
		Text:    "ok",
	}, nil
}
