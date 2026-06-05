package llm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/prompt"
	"github.com/superquail/langquail/tool"
)

type promptNodeState struct {
	Name string
}

func TestLLMNodeRendersPromptAndResolvesToolIDsFromContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "answer.md"), []byte("---\nrole: user\n---\nHello {{.Name}}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	prompts, err := prompt.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	tools := tool.NewRegistry()
	if err := tools.Register(tool.Define[map[string]string, map[string]string]("lookup").Execute(func(ctx context.Context, input map[string]string) (map[string]string, error) {
		return map[string]string{"ok": "true"}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	provider := &directProvider{name: "fake"}
	node := llm.Node("answer", llm.NodeSpec[promptNodeState]{
		Provider: "fake",
		Model:    "fake-model",
		PromptID: "answer",
		Data: func(ctx context.Context, state promptNodeState) (map[string]any, error) {
			return map[string]any{"Name": state.Name}, nil
		},
		ToolIDs: []string{"lookup"},
	})

	ctx := llm.WithProviders(context.Background(), llm.Providers(provider))
	ctx = prompt.WithRegistry(ctx, prompts)
	ctx = llm.WithToolSpecResolver(ctx, tools)
	if _, err := node.Run(ctx, promptNodeState{Name: "LangQuail"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %#v", provider.requests)
	}
	request := provider.requests[0]
	if len(request.Messages) != 1 || request.Messages[0].Role != llm.RoleUser || request.Messages[0].Content != "Hello LangQuail" {
		t.Fatalf("messages = %#v", request.Messages)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" {
		t.Fatalf("tools = %#v", request.Tools)
	}
}

func TestLLMNodeRejectsAmbiguousPromptAndToolConfiguration(t *testing.T) {
	node := llm.Node("answer", llm.NodeSpec[promptNodeState]{
		Providers: llm.Providers(&directProvider{name: "fake"}),
		Provider:  "fake",
		Model:     "fake-model",
		PromptID:  "answer",
		Messages: llm.MessagePolicy[promptNodeState]{
			Read: func(ctx context.Context, state promptNodeState) ([]llm.Message, error) {
				return []llm.Message{llm.User("hello")}, nil
			},
		},
	})
	_, err := node.Run(context.Background(), promptNodeState{})
	if err == nil || !strings.Contains(err.Error(), "PromptID and Messages.Read") {
		t.Fatalf("Run() error = %v", err)
	}

	node = llm.Node("answer", llm.NodeSpec[promptNodeState]{
		Providers: llm.Providers(&directProvider{name: "fake"}),
		Provider:  "fake",
		Model:     "fake-model",
		Messages: llm.MessagePolicy[promptNodeState]{
			Read: func(ctx context.Context, state promptNodeState) ([]llm.Message, error) {
				return []llm.Message{llm.User("hello")}, nil
			},
		},
		Tools:   []llm.ToolSpec{{Name: "legacy"}},
		ToolIDs: []string{"lookup"},
	})
	_, err = node.Run(context.Background(), promptNodeState{})
	if err == nil || !strings.Contains(err.Error(), "Tools and ToolIDs") {
		t.Fatalf("Run() error = %v", err)
	}
}
