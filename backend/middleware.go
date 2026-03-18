package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// ─── JSON response helpers ───────────────────────────────────────────────────

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ─── CORS wrapper ────────────────────────────────────────────────────────────

type corsHandler struct{ mux *http.ServeMux }

func (h corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// ─── Request logging ─────────────────────────────────────────────────────────

const ctxRequestID contextKey = "request_id"

type loggingHandler struct{ next http.Handler }

func (h loggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := generateRequestID()
	ctx := context.WithValue(r.Context(), ctxRequestID, requestID)
	r = r.WithContext(ctx)

	lw := &statusWriter{ResponseWriter: w, status: 200}
	h.next.ServeHTTP(lw, r)

	duration := time.Since(start)
	route := normalizePath(r.URL.Path)

	slog.Info("request completed",
		"method", r.Method,
		"path", r.URL.Path,
		"route", route,
		"status", lw.status,
		"duration_ms", duration.Milliseconds(),
		"request_id", requestID,
	)

	// Prometheus metrics
	httpRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(lw.status)).Inc()
	httpRequestDuration.WithLabelValues(r.Method, route).Observe(duration.Seconds())
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
