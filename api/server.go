package api

import (
	"bytes"
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
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool/skill"
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

type skillApplication interface {
	SkillRegistry() *skill.Registry
}

type ServeOption func(*serveConfig)

type serveConfig struct {
	configDir           string
	token               string
	tokenFile           string
	tokenSource         string
	tokenSourceConflict bool
	output              io.Writer
}

func WithConfigDir(path string) ServeOption {
	return func(config *serveConfig) {
		config.setTokenSource("config_dir")
		config.configDir = path
	}
}

func WithToken(token string) ServeOption {
	return func(config *serveConfig) {
		config.setTokenSource("token")
		config.token = token
	}
}

func WithTokenFile(path string) ServeOption {
	return func(config *serveConfig) {
		config.setTokenSource("token_file")
		config.tokenFile = path
	}
}

func WithOutput(output io.Writer) ServeOption {
	return func(config *serveConfig) {
		config.output = output
	}
}

func (config *serveConfig) setTokenSource(source string) {
	if config.tokenSource != "" {
		config.tokenSourceConflict = true
		return
	}
	config.tokenSource = source
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
	token, tokenPath, err := serverToken(config)
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
		if s.tokenPath != "" {
			fmt.Fprintf(s.output, "LangQuail server token file: %s\n", s.tokenPath)
		}
		fmt.Fprintf(s.output, "LangQuail server token: %s\n", s.token)
	}
	return http.ListenAndServe(addr, s.Handler())
}

func serverToken(config serveConfig) (string, string, error) {
	if config.tokenSourceConflict {
		return "", "", errors.New("api: multiple token sources configured")
	}
	switch config.tokenSource {
	case "":
		token, err := generateToken()
		return token, "", err
	case "token":
		token := strings.TrimSpace(config.token)
		if token == "" {
			return "", "", errors.New("api: token is required")
		}
		return token, "", nil
	case "token_file":
		return loadOrCreateTokenFile(config.tokenFile)
	case "config_dir":
		return loadOrCreateToken(config.configDir)
	default:
		return "", "", fmt.Errorf("api: unknown token source %q", config.tokenSource)
	}
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
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "skills":
		s.handleSkills(w, r)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "skills":
		s.handleSkillGet(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "runs" && parts[2] == "invoke":
		s.handleInvoke(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "runs" && parts[2] == "resume":
		s.handleResume(w, r, parts[1])
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

func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	registry := s.skillRegistry()
	if registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []skillSummary{}})
		return
	}
	skills := registry.List()
	summaries := make([]skillSummary, 0, len(skills))
	for _, item := range skills {
		summaries = append(summaries, summarizeSkill(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": summaries})
}

func (s *Server) handleSkillGet(w http.ResponseWriter, _ *http.Request, id string) {
	registry := s.skillRegistry()
	if registry == nil {
		writeError(w, http.StatusNotFound, "skill registry not configured")
		return
	}
	item, exists := registry.Get(id)
	if !exists {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
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
	defer r.Body.Close()
	input, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stateInput, invokeOptions, err := parseInvokeBody(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := s.app.Context(r.Context())
	ctx = withInvokeEventHandler(ctx, func(_ context.Context, event trace.Event) error {
		s.hub.Publish(event)
		return nil
	})
	output, err := executable.InvokeJSON(ctx, stateInput, invokeOptions...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRawJSON(w, http.StatusOK, output)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, workflowID string) {
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
	var request ResumeJSONRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := s.app.Context(r.Context())
	ctx = withInvokeEventHandler(ctx, func(_ context.Context, event trace.Event) error {
		s.hub.Publish(event)
		return nil
	})
	output, err := executable.ResumeJSON(ctx, request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRawJSON(w, http.StatusOK, output)
}

func parseInvokeBody(input []byte) (json.RawMessage, []lqruntime.InvokeOption, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		return nil, nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || !isInvokeEnvelope(fields) {
		return json.RawMessage(input), nil, nil
	}
	var envelope invokeEnvelope
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, nil, err
	}
	options := make([]lqruntime.InvokeOption, 0, 4)
	if envelope.RunID != "" {
		options = append(options, lqruntime.WithRunID(envelope.RunID))
	}
	if envelope.SessionID != "" {
		options = append(options, lqruntime.WithSession(envelope.SessionID))
	}
	if envelope.StartAt != "" {
		options = append(options, lqruntime.WithStartAt(envelope.StartAt))
	}
	if len(envelope.Metadata) > 0 {
		options = append(options, lqruntime.WithMetadata(envelope.Metadata))
	}
	return envelope.State, options, nil
}

func isInvokeEnvelope(fields map[string]json.RawMessage) bool {
	if len(fields) == 0 {
		return false
	}
	if _, exists := fields["state"]; !exists {
		return false
	}
	for _, key := range []string{"run_id", "session_id", "metadata", "start_at"} {
		if _, exists := fields[key]; exists {
			return true
		}
	}
	return false
}

type invokeEnvelope struct {
	State     json.RawMessage   `json:"state"`
	RunID     string            `json:"run_id,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	StartAt   string            `json:"start_at,omitempty"`
}

type skillSummary struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	UI          *skill.UIMetadata `json:"ui,omitempty"`
	Resources   map[string]int    `json:"resources,omitempty"`
}

func (s *Server) skillRegistry() *skill.Registry {
	app, ok := s.app.(skillApplication)
	if !ok {
		return nil
	}
	return app.SkillRegistry()
}

func summarizeSkill(item skill.Skill) skillSummary {
	summary := skillSummary{
		ID:          item.ID,
		Description: item.Description,
		Metadata:    item.Metadata,
		UI:          item.UI,
	}
	if len(item.Resources) > 0 {
		summary.Resources = make(map[string]int)
		for _, resource := range item.Resources {
			summary.Resources[string(resource.Kind)]++
		}
	}
	return summary
}

func writeRawJSON(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
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
