package tool

import (
	"context"
	"errors"

	"github.com/superquail/langquail/hitl"
)

var ErrPermissionDenied = errors.New("tool: permission denied")

type PermissionPolicy[TIn any] func(context.Context, TIn) (hitl.Request, bool, error)

func RequireApproval[TIn any](reason string) PermissionPolicy[TIn] {
	return func(context.Context, TIn) (hitl.Request, bool, error) {
		return hitl.NewRequest(hitl.RequestKindToolPermission, reason, nil), true, nil
	}
}
