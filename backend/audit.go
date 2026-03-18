package main

import (
	"context"
	"log/slog"
)

// auditEvent writes a structured audit log entry.
// All audit entries have audit=true for easy Loki filtering:
//
//	{app="backend"} | json | audit = `true`
func auditEvent(ctx context.Context, event string, fields ...any) {
	requestID, _ := ctx.Value(ctxRequestID).(string)
	sub, _ := ctx.Value(ctxSub).(string)
	email, _ := ctx.Value(ctxEmail).(string)

	args := []any{
		"audit", true,
		"event", event,
		"sub", sub,
		"email", email,
		"request_id", requestID,
	}
	args = append(args, fields...)
	slog.Info("audit", args...)
}
