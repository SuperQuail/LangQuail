package langquail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/superquail/langquail/api"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/prompt"
	"github.com/superquail/langquail/tool"
)

type AppBuilder struct {
	projectID string
	providers []llm.Provider
	promptDir string
	tools     []tool.Executable
	workflows []graph.Workflow
	store     any
	adjuster  any
}

type App struct {
	projectID    string
	providers    llm.ProviderSet
	prompts      *prompt.Registry
	tools        *tool.Registry
	workflows    map[string]graph.Workflow
	store        any
	llmAdjuster  llm.Adjuster
	toolAdjuster tool.Adjuster
}

func New(projectID string) *AppBuilder {
	return &AppBuilder{projectID: projectID}
}

func (b *AppBuilder) Providers(providers ...llm.Provider) *AppBuilder {
	if b == nil {
		return b
	}
	b.providers = append(b.providers, providers...)
	return b
}

func (b *AppBuilder) Prompts(path string) *AppBuilder {
	if b == nil {
		return b
	}
	b.promptDir = path
	return b
}

func (b *AppBuilder) Tools(tools ...tool.Executable) *AppBuilder {
	if b == nil {
		return b
	}
	b.tools = append(b.tools, tools...)
	return b
}

func (b *AppBuilder) Workflows(workflows ...graph.Workflow) *AppBuilder {
	if b == nil {
		return b
	}
	b.workflows = append(b.workflows, workflows...)
	return b
}

func (b *AppBuilder) Store(store any) *AppBuilder {
	if b == nil {
		return b
	}
	b.store = store
	return b
}

func (b *AppBuilder) Adjuster(adjuster any) *AppBuilder {
	if b == nil {
		return b
	}
	b.adjuster = adjuster
	return b
}

func (b *AppBuilder) Build() (*App, error) {
	if b == nil {
		return nil, errors.New("langquail: nil app builder")
	}
	if b.projectID == "" {
		return nil, errors.New("langquail: project id is required")
	}

	registrations := make([]llm.ProviderRegistration, 0, len(b.providers))
	for _, provider := range b.providers {
		registrations = append(registrations, llm.RegisterProvider(provider))
	}
	providers, err := llm.NewProviderSet(registrations...)
	if err != nil {
		return nil, err
	}

	var prompts *prompt.Registry
	if b.promptDir != "" {
		prompts, err = prompt.LoadDir(b.promptDir)
		if err != nil {
			return nil, err
		}
	}

	tools := tool.NewRegistry()
	for _, executable := range b.tools {
		if err := tools.Register(executable); err != nil {
			return nil, err
		}
	}

	var llmAdjuster llm.Adjuster
	var toolAdjuster tool.Adjuster
	if b.adjuster != nil {
		llmAdjuster, _ = b.adjuster.(llm.Adjuster)
		toolAdjuster, _ = b.adjuster.(tool.Adjuster)
		if llmAdjuster == nil && toolAdjuster == nil {
			return nil, errors.New("langquail: adjuster must implement llm.Adjuster, tool.Adjuster, or both")
		}
	}

	workflows := make(map[string]graph.Workflow, len(b.workflows))
	for _, workflow := range b.workflows {
		if workflow == nil {
			return nil, errors.New("langquail: nil workflow")
		}
		id := workflow.WorkflowID()
		if id == "" {
			return nil, errors.New("langquail: workflow id is required")
		}
		if _, exists := workflows[id]; exists {
			return nil, fmt.Errorf("langquail: duplicate workflow %q", id)
		}
		if err := workflow.Validate(); err != nil {
			return nil, err
		}
		workflows[id] = workflow
	}

	return &App{
		projectID:    b.projectID,
		providers:    providers,
		prompts:      prompts,
		tools:        tools,
		workflows:    workflows,
		store:        b.store,
		llmAdjuster:  llmAdjuster,
		toolAdjuster: toolAdjuster,
	}, nil
}

func (a *App) ProjectID() string {
	if a == nil {
		return ""
	}
	return a.projectID
}

func (a *App) ProviderSet() llm.ProviderSet {
	if a == nil {
		return llm.ProviderSet{}
	}
	return a.providers
}

func (a *App) PromptRegistry() *prompt.Registry {
	if a == nil {
		return nil
	}
	return a.prompts
}

func (a *App) ToolRegistry() *tool.Registry {
	if a == nil {
		return nil
	}
	return a.tools
}

func (a *App) StoreConfig() any {
	if a == nil {
		return nil
	}
	return a.store
}

func (a *App) LLMAdjuster() llm.Adjuster {
	if a == nil {
		return nil
	}
	return a.llmAdjuster
}

func (a *App) ToolAdjuster() tool.Adjuster {
	if a == nil {
		return nil
	}
	return a.toolAdjuster
}

func (a *App) Workflow(id string) (graph.Workflow, bool) {
	if a == nil {
		return nil, false
	}
	workflow, exists := a.workflows[id]
	return workflow, exists
}

func (a *App) Workflows() []graph.Workflow {
	if a == nil {
		return nil
	}
	ids := make([]string, 0, len(a.workflows))
	for id := range a.workflows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]graph.Workflow, 0, len(ids))
	for _, id := range ids {
		result = append(result, a.workflows[id])
	}
	return result
}

func (a *App) Snapshot(id string) (graph.Snapshot, bool) {
	workflow, exists := a.Workflow(id)
	if !exists {
		return graph.Snapshot{}, false
	}
	return workflow.Snapshot(), true
}

func (a *App) Context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return ctx
	}
	ctx = llm.WithProviders(ctx, a.providers)
	ctx = llm.WithToolSpecResolver(ctx, a.tools)
	ctx = tool.WithRegistry(ctx, a.tools)
	if a.llmAdjuster != nil {
		ctx = llm.WithAdjuster(ctx, a.llmAdjuster)
	}
	if a.toolAdjuster != nil {
		ctx = tool.WithAdjuster(ctx, a.toolAdjuster)
	}
	if a.prompts != nil {
		ctx = prompt.WithRegistry(ctx, a.prompts)
	}
	return ctx
}

func (a *App) Server(opts ...api.ServeOption) (*api.Server, error) {
	return api.NewServer(a, opts...)
}

func (a *App) Handler(opts ...api.ServeOption) (http.Handler, error) {
	server, err := a.Server(opts...)
	if err != nil {
		return nil, err
	}
	return server.Handler(), nil
}

func (a *App) Serve(addr string, opts ...api.ServeOption) error {
	server, err := a.Server(opts...)
	if err != nil {
		return err
	}
	return server.Serve(addr)
}
