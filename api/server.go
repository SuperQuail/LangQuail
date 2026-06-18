package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/coder/websocket"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/prompt"
	"github.com/superquail/langquail/trace"
)

type Application interface {
	ProjectID() string
	PromptRegistry() *prompt.Registry
	Workflow(string) (graph.Workflow, bool)
	Workflows() []graph.Workflow
	Snapshot(string) (graph.Snapshot, bool)
	Context(context.Context) context.Context
}

type ServeOption func(*serveConfig)

type serveConfig struct {
	configDir string
	output    io.Writer
}

func WithConfigDir(path string) ServeOption {
	return func(config *serveConfig) {
		config.configDir = path
	}
}

func WithOutput(output io.Writer) ServeOption {
	return func(config *serveConfig) {
		config.output = output
	}
}

type Server struct {
	app       Application
	token     string
	tokenPath string
	hub       *EventHub
	output    io.Writer
}

func NewServer(app Application, opts ...ServeOption) (*Server, error) {
	if app == nil {
		return nil, errors.New("api: app is required")
	}
	config := serveConfig{output: os.Stdout}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	token, tokenPath, err := loadOrCreateToken(config.configDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		app:       app,
		token:     token,
		tokenPath: tokenPath,
		hub:       NewEventHub(),
		output:    config.output,
	}, nil
}

func (s *Server) Token() string {
	if s == nil {
		return ""
	}
	return s.token
}

func (s *Server) TokenPath() string {
	if s == nil {
		return ""
	}
	return s.tokenPath
}

func (s *Server) EventHub() *EventHub {
	if s == nil {
		return nil
	}
	return s.hub
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) Serve(addr string) error {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	if s.output != nil {
		fmt.Fprintf(s.output, "LangQuail server token file: %s\n", s.tokenPath)
		fmt.Fprintf(s.output, "LangQuail server token: %s\n", s.token)
	}
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"project_id": s.app.ProjectID(),
		})
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/ws/events" {
		s.serveEventsWS(w, r)
		return
	}

	parts := pathParts(r.URL.Path)
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "workflows":
		s.handleWorkflows(w, r)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "workflows" && parts[2] == "snapshot":
		s.handleWorkflowSnapshot(w, r, parts[1])
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "prompts":
		s.handlePrompts(w, r)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "prompts":
		s.handlePromptGet(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "prompts" && parts[2] == "render":
		s.handlePromptRender(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "prompts" && parts[2] == "drafts":
		s.handlePromptDraft(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "runs" && parts[2] == "invoke":
		s.handleInvoke(w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleWorkflows(w http.ResponseWriter, _ *http.Request) {
	workflows := s.app.Workflows()
	ids := make([]string, 0, len(workflows))
	for _, workflow := range workflows {
		ids = append(ids, workflow.WorkflowID())
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": ids})
}

func (s *Server) handleWorkflowSnapshot(w http.ResponseWriter, _ *http.Request, id string) {
	snapshot, exists := s.app.Snapshot(id)
	if !exists {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handlePrompts(w http.ResponseWriter, _ *http.Request) {
	registry := s.app.PromptRegistry()
	if registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"prompts": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": registry.IDs()})
}

func (s *Server) handlePromptGet(w http.ResponseWriter, _ *http.Request, id string) {
	registry := s.app.PromptRegistry()
	if registry == nil {
		writeError(w, http.StatusNotFound, "prompt registry not configured")
		return
	}
	promptValue, err := registry.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, promptValue)
}

func (s *Server) handlePromptRender(w http.ResponseWriter, r *http.Request, id string) {
	registry := s.app.PromptRegistry()
	if registry == nil {
		writeError(w, http.StatusNotFound, "prompt registry not configured")
		return
	}
	var data map[string]any
	if err := decodeJSON(r, &data); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rendered, err := registry.Render(r.Context(), id, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rendered)
}

func (s *Server) handlePromptDraft(w http.ResponseWriter, r *http.Request, id string) {
	registry := s.app.PromptRegistry()
	if registry == nil {
		writeError(w, http.StatusNotFound, "prompt registry not configured")
		return
	}
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if contentRaw, ok := raw["content"]; ok {
		var content string
		if err := json.Unmarshal(contentRaw, &content); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := registry.SaveDraftText(id, content); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	body, err := json.Marshal(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var draft prompt.Draft
	if err := json.Unmarshal(body, &draft); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := registry.SaveDraft(id, draft); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request, workflowID string) {
	workflow, exists := s.app.Workflow(workflowID)
	if !exists {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	executable, ok := workflow.(ExecutableWorkflow)
	if !ok {
		writeError(w, http.StatusBadRequest, "workflow is not HTTP executable")
		return
	}
	input, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := s.app.Context(r.Context())
	ctx = withInvokeEventHandler(ctx, func(_ context.Context, event trace.Event) error {
		s.hub.Publish(event)
		return nil
	})
	output, err := executable.InvokeJSON(ctx, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

func (s *Server) serveEventsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	sub := s.hub.Subscribe(EventFilter{
		WorkflowID: r.URL.Query().Get("workflow_id"),
		RunID:      r.URL.Query().Get("run_id"),
		NodeID:     r.URL.Query().Get("node_id"),
	})
	defer sub.Close()

	for {
		select {
		case event := <-sub.Events():
			message, err := json.Marshal(map[string]any{
				"type":  "event",
				"event": event,
			})
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, message); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func pathParts(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
