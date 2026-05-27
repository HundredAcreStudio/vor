// Package httpx holds tiny helpers shared by every route handler: JSON
// encoding with a content-type stamp, error responses with stable shape,
// route logging, and parameter helpers.
package httpx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// ErrorBody is the JSON shape returned for any 4xx / 5xx response.
type ErrorBody struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

// JSON writes v as JSON with status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Header already written; can't recover. Log via stdlib slog (the
		// caller-set logger isn't reachable here).
		slog.Default().Error("httpx: encode response", "err", err)
	}
}

// Error writes an ErrorBody at status with msg + optional details.
func Error(w http.ResponseWriter, status int, msg string, details ...string) {
	body := ErrorBody{Error: msg, Code: status}
	if len(details) > 0 {
		body.Details = details[0]
	}
	JSON(w, status, body)
}

// NotFound is the canonical 404 response. Use when an entity (repo /
// node / file) doesn't exist.
func NotFound(w http.ResponseWriter, what string) {
	Error(w, http.StatusNotFound, what+" not found")
}

// BadRequest writes 400 with an explanation.
func BadRequest(w http.ResponseWriter, msg string) {
	Error(w, http.StatusBadRequest, msg)
}

// Internal writes 500. The error is logged via slog.Default(); the
// response keeps the user-visible detail short to avoid leaking internals.
func Internal(w http.ResponseWriter, err error) {
	slog.Default().Error("httpx: internal", "err", err)
	Error(w, http.StatusInternalServerError, "internal server error")
}

// ParseIntQuery reads ?name= as int, returning fallback when absent or
// unparseable.
func ParseIntQuery(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// URLParam is a thin re-export of chi's path-param helper so handlers
// don't have to depend on chi directly.
func URLParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// IsNoRows reports whether err is sql.ErrNoRows. Tiny wrapper because
// callers should never import "database/sql" just for this check.
func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// statusRecorder captures the response status for the logging middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Logging returns a chi-compatible middleware that emits one slog Info
// line per request: method, path, status, duration.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recovery catches panics in handlers and translates them to 500. Logs
// the stack so we can chase the root cause.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("http: panic",
						"path", r.URL.Path, "panic", rv,
						"stack", string(debug.Stack()))
					Error(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
