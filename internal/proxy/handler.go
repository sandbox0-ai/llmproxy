package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sandbox0-ai/llmproxy/internal/anthropic"
	"github.com/sandbox0-ai/llmproxy/internal/openairesp"
)

const maxBodyBytes = 50 * 1024 * 1024
const maxResponseBytes = 100 * 1024 * 1024

type Config struct {
}

type Handler struct {
	client *http.Client
}

func NewHandler(cfg Config) *Handler {
	client := newSecureHTTPClient()
	return &Handler{
		client: client,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	switch {
	case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	case strings.HasPrefix(r.URL.Path, claude2CodexPrefix):
		h.handleClaude2Codex(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) handleClaude2Codex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	route, err := parseClaude2CodexRoute(r.URL.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if route.Endpoint == "/responses/compact" {
		writeJSONError(w, http.StatusNotImplemented, "responses compact is not implemented yet")
		return
	}
	upstreamURL := route.AnthropicMessagesURL

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	defer r.Body.Close()

	var req openairesp.Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "model is required")
		return
	}
	converted, err := convertResponsesToAnthropic(req, req.Model)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	resp, err := h.callAnthropic(ctx, r, upstreamURL, converted.Request, req.Stream)
	if err != nil {
		slog.Warn("claude2codex upstream failed", "upstream", upstreamURL, "error", err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := convertAnthropicToResponses(resp, req.Model)
	if req.Stream {
		streamFinalResponse(w, out)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) callAnthropic(ctx context.Context, original *http.Request, upstreamURL string, req anthropic.Request, stream bool) (anthropic.Response, error) {
	req.Stream = stream
	body, err := json.Marshal(req)
	if err != nil {
		return anthropic.Response{}, err
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return anthropic.Response{}, err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Anthropic-Version", firstNonEmpty(original.Header.Get("Anthropic-Version"), "2023-06-01"))
	if beta := original.Header.Get("Anthropic-Beta"); beta != "" {
		upReq.Header.Set("Anthropic-Beta", beta)
	}
	copyAuthToAnthropic(upReq.Header, original.Header)
	if req.Stream {
		upReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := h.client.Do(upReq)
	if err != nil {
		return anthropic.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		slog.Warn("anthropic upstream error", "status", resp.StatusCode, "body", string(raw))
		return anthropic.Response{}, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	if req.Stream {
		return readAnthropicStream(resp.Body)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return anthropic.Response{}, err
	}
	var out anthropic.Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return anthropic.Response{}, fmt.Errorf("invalid upstream response")
	}
	return out, nil
}

func copyAuthToAnthropic(dst, src http.Header) {
	if key := src.Get("X-Api-Key"); key != "" {
		dst.Set("X-Api-Key", key)
		return
	}
	if auth := src.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		_, token, _ := strings.Cut(auth, " ")
		token = strings.TrimSpace(token)
		dst.Set("X-Api-Key", token)
		dst.Set("Authorization", "Bearer "+token)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": message,
		},
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
}
