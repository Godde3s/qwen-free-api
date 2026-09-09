// count_tokens.go — Anthropic parity: /v1/messages/count_tokens.
//
// Claude Code, Cline and other Anthropic-protocol clients call this before
// sending messages to budget their context. There is no upstream round-trip:
// the answer is the bridge-standard rough estimate (~4 chars/token) over the
// JSON size of messages + system + tools.

package qbridge

import (
	"encoding/json"
	"io"
	"net/http"
)

func countTokensHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]interface{}{
			"type":  "error",
			"error": map[string]interface{}{"type": "invalid_request_error", "message": "method not allowed"},
		})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{
			"type":  "error",
			"error": map[string]interface{}{"type": "invalid_request_error", "message": "failed to read body"},
		})
		return
	}
	var req struct {
		Messages json.RawMessage `json:"messages"`
		System   json.RawMessage `json:"system"`
		Tools    json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(body, &req) != nil {
		writeJSON(w, 400, map[string]interface{}{
			"type":  "error",
			"error": map[string]interface{}{"type": "invalid_request_error", "message": "invalid JSON body"},
		})
		return
	}
	n := len(req.Messages) + len(req.System) + len(req.Tools)
	if n == 0 {
		n = len(body)
	}
	if n == 0 {
		writeJSON(w, 200, map[string]interface{}{"input_tokens": 0})
		return
	}
	// mirror estimateTokens without needing a fake string
	writeJSON(w, 200, map[string]interface{}{"input_tokens": (n + 3) / 4})
}
