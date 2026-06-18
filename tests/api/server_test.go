package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	lq "github.com/superquail/langquail"
	"github.com/superquail/langquail/api"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/trace"
)

type serverState struct {
	Value string `json:"value"`
}

func TestServerTokenLifecycle(t *testing.T) {
	app := buildServerTestApp(t, false, "")
	configDir := t.TempDir()

	first, err := app.Server(api.WithConfigDir(configDir), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(first) error = %v", err)
	}
	if !strings.HasPrefix(first.Token(), "lq_") {
		t.Fatalf("token = %q", first.Token())
	}
	if _, err := os.Stat(filepath.Join(configDir, "server.json")); err != nil {
		t.Fatalf("server.json stat error = %v", err)
	}

	second, err := app.Server(api.WithConfigDir(configDir), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(second) error = %v", err)
	}
	if second.Token() != first.Token() {
		t.Fatalf("token was not reused: first=%q second=%q", first.Token(), second.Token())
	}

	envDir := t.TempDir()
	t.Setenv(api.ConfigDirEnv, envDir)
	envServer, err := app.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(env) error = %v", err)
	}
	if !strings.HasPrefix(envServer.TokenPath(), envDir) {
		t.Fatalf("env token path = %q, want under %q", envServer.TokenPath(), envDir)
	}

	invalidDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidDir, "server.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	if _, err := app.Server(api.WithConfigDir(invalidDir), api.WithOutput(io.Discard)); err == nil {
		t.Fatal("Server(invalid) error is nil")
	}
}

func TestServerHTTPAuthAndPromptRoutes(t *testing.T) {
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "answer.md"), []byte("---\nrole: user\n---\nHello {{.Name}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(prompt) error = %v", err)
	}
	app := buildServerTestApp(t, false, promptDir)
	server, err := app.Server(api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	if status := statusCode(t, http.MethodGet, httpServer.URL+"/health", "", nil); status != http.StatusOK {
		t.Fatalf("GET /health status = %d", status)
	}
	if status := statusCode(t, http.MethodGet, httpServer.URL+"/workflows", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("GET /workflows without token status = %d", status)
	}
	if status := statusCode(t, http.MethodGet, httpServer.URL+"/workflows", "Bearer "+server.Token(), nil); status != http.StatusOK {
		t.Fatalf("GET /workflows bearer status = %d", status)
	}

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/prompts/answer/render", bytes.NewBufferString(`{"Name":"LangQuail"}`))
	if err != nil {
		t.Fatalf("NewRequest(render) error = %v", err)
	}
	req.Header.Set("X-LangQuail-Token", server.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(render) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("render status = %d body=%s", resp.StatusCode, body)
	}
	var rendered struct {
		Segments []struct {
			Content string `json:"content"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rendered); err != nil {
		t.Fatalf("decode render: %v", err)
	}
	if len(rendered.Segments) != 1 || rendered.Segments[0].Content != "Hello LangQuail" {
		t.Fatalf("rendered = %#v", rendered)
	}
}

func TestServerInvokeExecutableWorkflowAndRejectsPlainWorkflow(t *testing.T) {
	plainApp := buildServerTestApp(t, false, "")
	plainServer, err := plainApp.Server(api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("plain Server() error = %v", err)
	}
	plainHTTP := httptest.NewServer(plainServer.Handler())
	defer plainHTTP.Close()
	if status := statusCode(t, http.MethodPost, plainHTTP.URL+"/runs/server.workflow/invoke", "Bearer "+plainServer.Token(), strings.NewReader(`{"value":"x"}`)); status != http.StatusBadRequest {
		t.Fatalf("plain invoke status = %d", status)
	}

	execApp := buildServerTestApp(t, true, "")
	execServer, err := execApp.Server(api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("exec Server() error = %v", err)
	}
	execHTTP := httptest.NewServer(execServer.Handler())
	defer execHTTP.Close()

	req, err := http.NewRequest(http.MethodPost, execHTTP.URL+"/runs/server.workflow/invoke", strings.NewReader(`{"value":"ok"}`))
	if err != nil {
		t.Fatalf("NewRequest(invoke) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+execServer.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(invoke) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invoke status = %d body=%s", resp.StatusCode, body)
	}
	var result struct {
		State  serverState   `json:"state"`
		Events []trace.Event `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode invoke: %v", err)
	}
	if result.State.Value != "ok!" {
		t.Fatalf("state = %#v", result.State)
	}
	for _, event := range result.Events {
		if event.Context != nil {
			t.Fatalf("result event context = %#v, want nil", event.Context)
		}
	}
}

func TestServerWebSocketReceivesLiveEventContext(t *testing.T) {
	app := buildServerTestApp(t, true, "")
	server, err := app.Server(api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/events"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("Dial without token succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Dial without token status = %v err=%v", respStatus(resp), err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL+"?token="+server.Token(), nil)
	if err != nil {
		t.Fatalf("Dial with token error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/runs/server.workflow/invoke", strings.NewReader(`{"value":"ws"}`))
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("Authorization", "Bearer "+server.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			done <- &statusError{status: resp.StatusCode, body: string(body)}
			return
		}
		done <- nil
	}()

	_, message, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read websocket error = %v", err)
	}
	var envelope struct {
		Type  string      `json:"type"`
		Event trace.Event `json:"event"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	if envelope.Type != "event" || envelope.Event.Context == nil || len(envelope.Event.Context.Current.State) == 0 {
		t.Fatalf("websocket envelope = %#v", envelope)
	}
	if err := <-done; err != nil {
		t.Fatalf("invoke error = %v", err)
	}
}

func buildServerTestApp(t *testing.T, executable bool, promptDir string) *lq.App {
	t.Helper()
	g := graph.NewStateGraph[serverState]("server.workflow")
	g.Step("start", func(ctx context.Context, state serverState) (graph.Command[serverState], error) {
		state.Value += "!"
		return graph.Update(state), nil
	})
	g.Start("start")
	g.Finish("start")

	builder := lq.New("server-project")
	if promptDir != "" {
		builder.Prompts(promptDir)
	}
	if executable {
		builder.Workflows(lq.Executable(g))
	} else {
		builder.Workflows(g)
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return app
}

func statusCode(t *testing.T, method string, url string, authorization string, body io.Reader) int {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return strings.TrimSpace(e.body)
}
