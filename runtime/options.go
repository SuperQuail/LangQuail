package runtime

import (
	"context"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/trace"
)

type Option[S any] func(*Runner[S])

type EventHandler func(context.Context, trace.Event) error

func WithRecorder[S any](recorder trace.Recorder) Option[S] {
	return func(r *Runner[S]) {
		if recorder != nil {
			r.recorder = recorder
		}
	}
}

func WithCheckpointStore[S any](store checkpoint.Store) Option[S] {
	return func(r *Runner[S]) {
		if store != nil {
			r.checkpoints = store
		}
	}
}

func WithSerializer[S any](serializer checkpoint.Serializer[S]) Option[S] {
	return func(r *Runner[S]) {
		if serializer != nil {
			r.serializer = serializer
		}
	}
}

func WithEventHandler[S any](handler EventHandler) Option[S] {
	return func(r *Runner[S]) {
		r.onEvent = handler
	}
}

func WithMaxSteps[S any](maxSteps int) Option[S] {
	return func(r *Runner[S]) {
		r.maxSteps = maxSteps
	}
}

type InvokeOption func(*invokeConfig)

type invokeConfig struct {
	runID     string
	sessionID string
	startAt   string
	metadata  map[string]string
}

func WithRunID(runID string) InvokeOption {
	return func(config *invokeConfig) {
		config.runID = runID
	}
}

func WithSession(sessionID string) InvokeOption {
	return func(config *invokeConfig) {
		config.sessionID = sessionID
	}
}

func WithStartAt(nodeID string) InvokeOption {
	return func(config *invokeConfig) {
		config.startAt = nodeID
	}
}

func WithMetadata(metadata map[string]string) InvokeOption {
	return func(config *invokeConfig) {
		if len(metadata) == 0 {
			return
		}
		config.metadata = cloneMetadata(metadata)
	}
}
