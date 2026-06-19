package tool

import (
	"context"
	"sync"
	"time"

	"github.com/superquail/langquail/trace"
)

const defaultProgressInterval = 300 * time.Millisecond

type progressPayload struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func startProgressTicker(ctx context.Context, call Call, startedAt time.Time, interval time.Duration) func() {
	interval = normalizeProgressInterval(interval)
	if ctx == nil || interval < 0 {
		return func() {}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case now := <-ticker.C:
				_, _ = trace.Emit(ctx, trace.EventToolProgress, progressPayload{
					CallID:    call.ID,
					Name:      call.Name,
					ElapsedMS: elapsedMillisecondsAt(startedAt, now),
				})
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

func normalizeProgressInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return defaultProgressInterval
	}
	return interval
}

func elapsedMilliseconds(startedAt time.Time) int64 {
	return elapsedMillisecondsAt(startedAt, time.Now())
}

func elapsedMillisecondsAt(startedAt time.Time, now time.Time) int64 {
	if startedAt.IsZero() {
		return 0
	}
	elapsed := now.Sub(startedAt).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
