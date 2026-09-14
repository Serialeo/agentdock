package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/runtimeapi"
)

func registerRuntimeAPI(mux *http.ServeMux, runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) {
	h := runtimeAPIHandler(runtime, cfg, oauthStore)
	mux.HandleFunc("/internal/runtime/status", h)
	mux.HandleFunc("/internal/runtime/builtins", h)
	mux.HandleFunc("/internal/runtime/capabilities", h)
	mux.HandleFunc("/internal/runtime/files", h)
	mux.HandleFunc("/internal/runtime/skills", h)
	mux.HandleFunc("/internal/runtime/skills/", h)
	mux.HandleFunc("/internal/runtime/tasks", h)
	mux.HandleFunc("/internal/runtime/tasks/", h)
	mux.HandleFunc("/internal/runtime/evolve", h)
	mux.HandleFunc("/internal/runtime/mcp", h)
	mux.HandleFunc("/internal/runtime/mcp/", h)
}

func runtimeAPIHandler(runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) http.HandlerFunc {
	authorizer := auth.Bearer{Token: cfg.AuthToken}
	authRequired := cfg.AuthRequired()
	return func(w http.ResponseWriter, r *http.Request) {
		if !runtimeapi.MethodAllowed(r.Method, r.URL.Path) {
			w.Header().Set("Allow", runtimeapi.AllowHeader(r.URL.Path))
			writeRuntimeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		staticOK := cfg.AuthToken != "" && authorizer.Authorized(r)
		oauthOK := authorizedOAuth(r, cfg, oauthStore)
		if authRequired && !staticOK && !oauthOK {
			setBearerChallenge(w, cfg, r, strings.TrimSpace(r.Header.Get("Authorization")) != "")
			writeRuntimeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
			return
		}

		body, err := runtimeRequestBody(r)
		if err != nil {
			writeRuntimeAPIError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read runtime request body")
			return
		}
		timeout := 8 * time.Second
		if r.URL.Path == "/internal/runtime/builtins" {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		result, err := runtimeapi.Dispatch(ctx, runtime, runtimeapi.Request{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Body:   body,
		})
		if err != nil {
			writeRuntimeAPIHandlerError(w, err)
			return
		}
		writeJSON(w, result)
	}
}

func runtimeRequestBody(r *http.Request) ([]byte, error) {
	cleanPath := strings.TrimSuffix(r.URL.Path, "/")
	if r.Method != http.MethodPost {
		return nil, nil
	}
	limit := int64(64 * 1024)
	switch cleanPath {
	case "/internal/runtime/builtins", "/internal/runtime/mcp", "/internal/runtime/evolve", "/internal/runtime/skills/manage":
	default:
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("runtime request body is too large")
	}
	return data, nil
}

func writeRuntimeAPIHandlerError(w http.ResponseWriter, err error) {
	var toolErr *app.ToolError
	if errors.As(err, &toolErr) {
		status := http.StatusInternalServerError
		switch toolErr.Category {
		case "validation":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		}
		writeRuntimeAPIError(w, status, toolErr.Code, toolErr.Message)
		return
	}
	writeRuntimeAPIError(w, http.StatusInternalServerError, "RUNTIME_API_ERROR", err.Error())
}

func writeRuntimeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "error": message}); err != nil {
		slog.Warn("write runtime API error response failed", "status", status, "code", code, "error", err)
	}
}
