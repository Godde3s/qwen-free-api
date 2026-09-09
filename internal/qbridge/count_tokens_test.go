// count_tokens_test.go — Anthropic parity endpoint.

package qbridge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCountTokensHandler(t *testing.T) {
	body := `{"model":"x","messages":[{"role":"user","content":"hello world this is a counting test message"}],"system":"sys"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	countTokensHandler(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// payload is ~110 chars → ~28 tokens
	if out.InputTokens < 20 || out.InputTokens > 60 {
		t.Fatalf("input_tokens = %d, want ~20-60", out.InputTokens)
	}
}

func TestCountTokensBadJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader([]byte("garbage")))
	w := httptest.NewRecorder()
	countTokensHandler(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCountTokensMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/messages/count_tokens", nil)
	w := httptest.NewRecorder()
	countTokensHandler(w, req)
	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
