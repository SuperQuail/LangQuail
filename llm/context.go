package llm

import "context"

type providerSetContextKey struct{}

func WithProviders(ctx context.Context, providers ProviderSet) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, providerSetContextKey{}, providers)
}

func ProvidersFromContext(ctx context.Context) (ProviderSet, bool) {
	if ctx == nil {
		return ProviderSet{}, false
	}
	providers, ok := ctx.Value(providerSetContextKey{}).(ProviderSet)
	return providers, ok
}

type ToolSpecResolver interface {
	LLMSpecs(names ...string) ([]ToolSpec, error)
}

type toolSpecResolverContextKey struct{}

func WithToolSpecResolver(ctx context.Context, resolver ToolSpecResolver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if resolver == nil {
		return ctx
	}
	return context.WithValue(ctx, toolSpecResolverContextKey{}, resolver)
}

func ToolSpecResolverFromContext(ctx context.Context) (ToolSpecResolver, bool) {
	if ctx == nil {
		return nil, false
	}
	resolver, ok := ctx.Value(toolSpecResolverContextKey{}).(ToolSpecResolver)
	return resolver, ok && resolver != nil
}
