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
	"github.com/superquail/langquail/hitl"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/tool/skill"
	"github.com/superquail/langquail/trace"
)

type serverState struct {
	Value string `json:"value"`
}

type resumeServerState struct {
	Value string   `json:"value"`
	Path  []string `json:"path,omitempty"`
}

type serverToolState struct {
	Calls   []tool.Call   `json:"calls"`
	Results []tool.Result `json:"results,omitempty"`
}

type serverToolInput struct {
	Query string `json:"query"`
}

func TestServerTokenLifecycle(t *testing.T) {
	app := buildServerTestApp(t, false, "")

	envDir := t.TempDir()
	t.Setenv(api.ConfigDirEnv, envDir)
	defaultServer, err := app.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(default) error = %v", err)
	}
	if !strings.HasPrefix(defaultServer.Token(), "lq_") {
		t.Fatalf("default token = %q", defaultServer.Token())
	}
	if defaultServer.TokenPath() != "" {
		t.Fatalf("default token path = %q, want empty", defaultServer.TokenPath())
	}
	if _, err := os.Stat(filepath.Join(envDir, "server.json")); !os.IsNotExist(err) {
		t.Fatalf("default server.json stat error = %v, want not exist", err)
	}

	callerToken, err := app.Server(api.WithToken("caller-token"), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(token) error = %v", err)
	}
	if callerToken.Token() != "caller-token" || callerToken.TokenPath() != "" {
		t.Fatalf("caller token=%q path=%q", callerToken.Token(), callerToken.TokenPath())
	}
	if _, err := app.Server(api.WithToken(" "), api.WithOutput(io.Discard)); err == nil {
		t.Fatal("Server(empty token) error is nil")
	}

	configDir := t.TempDir()
	first, err := app.Server(api.WithConfigDir(configDir), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(first config) error = %v", err)
	}
	if !strings.HasPrefix(first.Token(), "lq_") {
		t.Fatalf("config token = %q", first.Token())
	}
	if _, err := os.Stat(filepath.Join(configDir, "server.json")); err != nil {
		t.Fatalf("config server.json stat error = %v", err)
	}

	second, err := app.Server(api.WithConfigDir(configDir), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(second config) error = %v", err)
	}
	if second.Token() != first.Token() {
		t.Fatalf("token was not reused: first=%q second=%q", first.Token(), second.Token())
	}

	envServer, err := app.Server(api.WithConfigDir(""), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(env) error = %v", err)
	}
	if !strings.HasPrefix(envServer.TokenPath(), envDir) {
		t.Fatalf("env token path = %q, want under %q", envServer.TokenPath(), envDir)
	}

	tokenFile := filepath.Join(t.TempDir(), "custom-token.json")
	fileServer, err := app.Server(api.WithTokenFile(tokenFile), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(token file) error = %v", err)
	}
	if fileServer.TokenPath() != tokenFile {
		t.Fatalf("token file path = %q, want %q", fileServer.TokenPath(), tokenFile)
	}
	fileServerAgain, err := app.Server(api.WithTokenFile(tokenFile), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server(token file again) error = %v", err)
	}
	if fileServerAgain.Token() != fileServer.Token() {
		t.Fatalf("token file was not reused: first=%q second=%q", fileServer.Token(), fileServerAgain.Token())
	}

	invalidDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidDir, "server.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	if _, err := app.Server(api.WithConfigDir(invalidDir), api.WithOutput(io.Discard)); err == nil {
		t.Fatal("Server(invalid) error is nil")
	}
	if _, err := app.Server(api.WithToken("caller-token"), api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard)); err == nil {
		t.Fatal("Server(multiple token sources) error is nil")
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

func TestServerSkillRoutes(t *testing.T) {
	emptyApp := buildServerTestApp(t, false, "")
	emptyServer, err := emptyApp.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("empty Server() error = %v", err)
	}
	emptyHTTP := httptest.NewServer(emptyServer.Handler())
	defer emptyHTTP.Close()
	req, err := http.NewRequest(http.MethodGet, emptyHTTP.URL+"/skills", nil)
	if err != nil {
		t.Fatalf("NewRequest(empty skills) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+emptyServer.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(empty skills) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("empty skills status = %d body=%s", resp.StatusCode, body)
	}
	var emptyList struct {
		Skills []skillSummaryResponse `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emptyList); err != nil {
		t.Fatalf("decode empty skills: %v", err)
	}
	if len(emptyList.Skills) != 0 {
		t.Fatalf("empty skills = %#v", emptyList.Skills)
	}

	skillRoot := t.TempDir()
	writeServerSkill(t, skillRoot, "coder", "Write code")
	app, err := lq.New("server-project").SkillDirs(skillRoot).Build()
	if err != nil {
		t.Fatalf("Build(skill app) error = %v", err)
	}
	server, err := app.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	req, err = http.NewRequest(http.MethodGet, httpServer.URL+"/skills", nil)
	if err != nil {
		t.Fatalf("NewRequest(skills) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+server.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(skills) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("skills status = %d body=%s", resp.StatusCode, body)
	}
	var listed struct {
		Skills []skillSummaryResponse `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode skills: %v", err)
	}
	if len(listed.Skills) != 1 || listed.Skills[0].ID != "coder" || listed.Skills[0].Resources["reference"] != 1 || listed.Skills[0].Resources["script"] != 1 {
		t.Fatalf("listed skills = %#v", listed.Skills)
	}

	req, err = http.NewRequest(http.MethodGet, httpServer.URL+"/skills/coder", nil)
	if err != nil {
		t.Fatalf("NewRequest(skill detail) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+server.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(skill detail) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("skill detail status = %d body=%s", resp.StatusCode, body)
	}
	var detail skill.Skill
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode skill detail: %v", err)
	}
	if detail.ID != "coder" || !strings.Contains(detail.Instructions, "Use code skill") || len(detail.Resources) != 2 {
		t.Fatalf("skill detail = %#v", detail)
	}

	if status := statusCode(t, http.MethodGet, httpServer.URL+"/skills/missing", "Bearer "+server.Token(), nil); status != http.StatusNotFound {
		t.Fatalf("missing skill status = %d", status)
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

	req, err = http.NewRequest(http.MethodPost, execHTTP.URL+"/runs/server.workflow/invoke", strings.NewReader(`{"state":{"value":"env"},"run_id":"run_http","session_id":"session_http","metadata":{"tenant":"acme"},"start_at":"start"}`))
	if err != nil {
		t.Fatalf("NewRequest(envelope invoke) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+execServer.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(envelope invoke) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("envelope invoke status = %d body=%s", resp.StatusCode, body)
	}
	var envelopeResult struct {
		Run   lqruntime.Run `json:"run"`
		State serverState   `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelopeResult); err != nil {
		t.Fatalf("decode envelope invoke: %v", err)
	}
	if envelopeResult.Run.ID != "run_http" || envelopeResult.Run.SessionID != "session_http" || envelopeResult.Run.Metadata["tenant"] != "acme" {
		t.Fatalf("envelope run = %#v", envelopeResult.Run)
	}
	if envelopeResult.State.Value != "env!" {
		t.Fatalf("envelope state = %#v", envelopeResult.State)
	}

	req, err = http.NewRequest(http.MethodPost, execHTTP.URL+"/runs/server.workflow/invoke", strings.NewReader(`{"state":{"value":"bad"},"run_id":"run_bad","unexpected":true}`))
	if err != nil {
		t.Fatalf("NewRequest(bad envelope) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+execServer.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(bad envelope) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad envelope status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, execHTTP.URL+"/runs/server.workflow/invoke", strings.NewReader(`{"state":{"value":"nested"},"value":"raw"}`))
	if err != nil {
		t.Fatalf("NewRequest(raw state field) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+execServer.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(raw state field) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("raw state field status = %d body=%s", resp.StatusCode, body)
	}
	var rawStateResult struct {
		State serverState `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawStateResult); err != nil {
		t.Fatalf("decode raw state field: %v", err)
	}
	if rawStateResult.State.Value != "raw!" {
		t.Fatalf("raw state field result = %#v", rawStateResult.State)
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

func TestServerWebSocketReceivesToolProgressEvent(t *testing.T) {
	app := buildToolProgressServerTestApp(t)
	server, err := app.Server(api.WithConfigDir(t.TempDir()), api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("Server() error = %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/events?token=" + server.Token()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	done := make(chan error, 1)
	go func() {
		body := `{"calls":[{"id":"call_ws","name":"lookup","arguments":{"query":"ws"}}]}`
		req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/runs/server.tool.progress/invoke", strings.NewReader(body))
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

	progress := readWebSocketEvent(t, ctx, conn, trace.EventToolProgress)
	var payload struct {
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		ElapsedMS int64  `json:"elapsed_ms"`
	}
	if err := json.Unmarshal(progress.Payload, &payload); err != nil {
		t.Fatalf("decode progress payload: %v", err)
	}
	if payload.CallID != "call_ws" || payload.Name != "lookup" || payload.ElapsedMS < 0 {
		t.Fatalf("progress payload = %#v", payload)
	}
	if err := <-done; err != nil {
		t.Fatalf("invoke error = %v", err)
	}
}

func TestServerResumeUsesCallerProvidedState(t *testing.T) {
	app := buildResumeServerTestApp(t)
	firstServer, err := app.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("first Server() error = %v", err)
	}
	firstHTTP := httptest.NewServer(firstServer.Handler())
	defer firstHTTP.Close()

	req, err := http.NewRequest(http.MethodPost, firstHTTP.URL+"/runs/server.resume/invoke", strings.NewReader(`{"value":"start"}`))
	if err != nil {
		t.Fatalf("NewRequest(invoke) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+firstServer.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(invoke) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invoke status = %d body=%s", resp.StatusCode, body)
	}
	var interrupted struct {
		Run   lqruntime.Run     `json:"run"`
		State resumeServerState `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&interrupted); err != nil {
		t.Fatalf("decode interrupted result: %v", err)
	}
	if interrupted.Run.Status != lqruntime.StatusInterrupted {
		t.Fatalf("interrupted run = %#v", interrupted.Run)
	}
	if interrupted.State.Value != "start:prepared" {
		t.Fatalf("interrupted state = %#v", interrupted.State)
	}

	secondServer, err := app.Server(api.WithOutput(io.Discard))
	if err != nil {
		t.Fatalf("second Server() error = %v", err)
	}
	secondHTTP := httptest.NewServer(secondServer.Handler())
	defer secondHTTP.Close()
	resumeBody, err := json.Marshal(map[string]any{
		"run":           interrupted.Run,
		"state":         interrupted.State,
		"resume_node":   "human",
		"response":      hitl.Provide("yes"),
		"interrupt_id":  "int_http",
		"checkpoint_id": "chk_http",
	})
	if err != nil {
		t.Fatalf("Marshal(resume) error = %v", err)
	}
	req, err = http.NewRequest(http.MethodPost, secondHTTP.URL+"/runs/server.resume/resume", bytes.NewReader(resumeBody))
	if err != nil {
		t.Fatalf("NewRequest(resume) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+secondServer.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(resume) error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("resume status = %d body=%s", resp.StatusCode, body)
	}
	var resumed struct {
		Run    lqruntime.Run     `json:"run"`
		State  resumeServerState `json:"state"`
		Events []trace.Event     `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed result: %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted || resumed.Run.ID != interrupted.Run.ID {
		t.Fatalf("resumed run = %#v interrupted = %#v", resumed.Run, interrupted.Run)
	}
	if resumed.State.Value != "start:prepared:yes" {
		t.Fatalf("resumed state = %#v", resumed.State)
	}
	event := requireAPIEvent(t, resumed.Events, trace.EventRunResumed)
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode run.resumed payload: %v", err)
	}
	if payload["interrupt_id"] != "int_http" || payload["checkpoint_id"] != "chk_http" || payload["resume_node"] != "human" {
		t.Fatalf("run.resumed payload = %#v", payload)
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

func buildResumeServerTestApp(t *testing.T) *lq.App {
	t.Helper()
	g := graph.NewStateGraph[resumeServerState]("server.resume")
	g.Step("prepare", func(ctx context.Context, state resumeServerState) (graph.Command[resumeServerState], error) {
		state.Value += ":prepared"
		state.Path = append(state.Path, "prepared")
		return graph.Update(state), nil
	})
	g.Node(hitl.Node("human", hitl.NodeSpec[resumeServerState]{
		Request: func(ctx context.Context, state resumeServerState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need input", nil), nil
		},
		Output: func(ctx context.Context, state resumeServerState, response hitl.Response) (graph.Command[resumeServerState], error) {
			answer, err := hitl.DecodePayload[string](response)
			if err != nil {
				return graph.Noop[resumeServerState](), err
			}
			state.Value += ":" + answer
			state.Path = append(state.Path, "human")
			return graph.Update(state), nil
		},
	}))
	g.Flow("prepare", "human")
	g.Start("prepare")
	g.Finish("human")

	app, err := lq.New("server-project").Workflows(lq.Executable(g)).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return app
}

func buildToolProgressServerTestApp(t *testing.T) *lq.App {
	t.Helper()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[serverToolInput, string]("lookup").
		Execute(func(ctx context.Context, input serverToolInput) (string, error) {
			time.Sleep(30 * time.Millisecond)
			return "found:" + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[serverToolState]("server.tool.progress")
	g.Node(tool.Node("run_tool", tool.NodeSpec[serverToolState]{
		Registry:         registry,
		ProgressInterval: 5 * time.Millisecond,
		Calls: func(ctx context.Context, state serverToolState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state serverToolState, results []tool.Result) (graph.Command[serverToolState], error) {
			state.Results = append(state.Results, results...)
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	app, err := lq.New("server-project").Workflows(lq.Executable(g)).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return app
}

func requireAPIEvent(t *testing.T, events []trace.Event, eventType string) trace.Event {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("event %s not found in %#v", eventType, events)
	return trace.Event{}
}

func readWebSocketEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, eventType string) trace.Event {
	t.Helper()
	for {
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
		if envelope.Type == "event" && envelope.Event.Type == eventType {
			return envelope.Event
		}
	}
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

type skillSummaryResponse struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Resources   map[string]int `json:"resources"`
}

func writeServerSkill(t *testing.T, root string, name string, description string) {
	t.Helper()
	skillDir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("MkdirAll(references) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("MkdirAll(scripts) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+description+"\n---\nUse code skill."), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(reference) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"), []byte("echo ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(script) error = %v", err)
	}
}
