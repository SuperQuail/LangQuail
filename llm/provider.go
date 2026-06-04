package llm

import (
	"context"
	"errors"
	"fmt"
)

type Provider interface {
	Name() string
	Chat(context.Context, Request) (Response, error)
}

type ProviderSet struct {
	providers map[string]Provider
}

func Providers(items ...Provider) ProviderSet {
	set := ProviderSet{providers: make(map[string]Provider)}
	for _, item := range items {
		if item == nil || item.Name() == "" {
			continue
		}
		set.providers[item.Name()] = item
	}
	return set
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
	return names
}
