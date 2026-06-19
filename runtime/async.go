package runtime

import (
	"context"
	"errors"
)

type AsyncResult[S any] struct {
	Result *Result[S]
	Error  error
}

type Invocation[S any] struct {
	done   chan AsyncResult[S]
	cancel context.CancelFunc
}

func (r *Runner[S]) InvokeAsync(ctx context.Context, initialState S, opts ...InvokeOption) *Invocation[S] {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	invocation := &Invocation[S]{
		done:   make(chan AsyncResult[S], 1),
		cancel: cancel,
	}
	go func() {
		defer cancel()
		if r == nil {
			invocation.done <- AsyncResult[S]{Error: errors.New("runtime: runner is required")}
			close(invocation.done)
			return
		}
		result, err := r.Invoke(ctx, initialState, opts...)
		invocation.done <- AsyncResult[S]{Result: result, Error: err}
		close(invocation.done)
	}()
	return invocation
}

func (i *Invocation[S]) Done() <-chan AsyncResult[S] {
	if i == nil {
		ch := make(chan AsyncResult[S])
		close(ch)
		return ch
	}
	return i.done
}

func (i *Invocation[S]) Cancel() {
	if i == nil || i.cancel == nil {
		return
	}
	i.cancel()
}
