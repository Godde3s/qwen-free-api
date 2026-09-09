// Qwen-specific unit tests: request body shape, SSE delta extraction,
// error classification and WAF detection.

package qbridge

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestBuildQwenBodyShape(t *testing.T) {
	body := buildQwenBody("chat-1", "qwen3.8-max", "hello", true)
	if body["chat_id"] != "chat-1" {
		t.Fatalf("chat_id = %v", body["chat_id"])
	}
	if body["model"] != "qwen3.8-max" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["chat_mode"] != "normal" {
		t.Fatalf("chat_mode = %v, want normal", body["chat_mode"])
	}
	if body["stream"] != true || body["incremental_output"] != true {
		t.Fatalf("stream flags wrong: %v", body)
	}
	msgs, ok := body["messages"].([]map[string]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages shape wrong")
	}
	if msgs[0]["role"] != "user" || msgs[0]["content"] != "hello" {
		t.Fatalf("message content wrong")
	}
	fc, ok := msgs[0]["feature_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("feature_config missing")
	}
	if fc["thinking_enabled"] != true {
		t.Fatalf("thinking_enabled should default true")
	}
}

func TestBuildQwenGuestBodyMode(t *testing.T) {
	body := buildQwenGuestBody("c2", "qwen3.7-plus", "hi", false)
	if body["chat_mode"] != "guest" {
		t.Fatalf("guest body chat_mode = %v", body["chat_mode"])
	}
	msgs := body["messages"].([]map[string]interface{})
	fc := msgs[0]["feature_config"].(map[string]interface{})
	if fc["thinking_enabled"] != false {
		t.Fatalf("thinking_enabled should be false when requested")
	}
}

func TestExtractQwenDeltaContentAndReasoning(t *testing.T) {
	raw := `{"choices":[{"delta":{"content":"سلام"},"finish_reason":null}]}`
	var ev qwenSSEEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	c, r, f := extractQwenDelta(&ev)
	if c != "سلام" || r != "" || f != "" {
		t.Fatalf("got c=%q r=%q f=%q", c, r, f)
	}
}

func TestExtractQwenDeltaThinkingSummary(t *testing.T) {
	// phase=thinking_summary carries reasoning inside extra.summary_thought
	raw := `{"choices":[{"delta":{"phase":"thinking_summary","extra":{"summary_thought":{"content":["فکر ","اول"]}}}}]}`
	var ev qwenSSEEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	c, r, _ := extractQwenDelta(&ev)
	if c != "" {
		t.Fatalf("content should be empty, got %q", c)
	}
	if r != "فکر اول" {
		t.Fatalf("reasoning = %q, want joined array", r)
	}
}

func TestExtractQwenDeltaReasoningField(t *testing.T) {
	raw := `{"choices":[{"delta":{"reasoning_content":"گام اول"}}]}`
	var ev qwenSSEEvent
	json.Unmarshal([]byte(raw), &ev)
	_, r, _ := extractQwenDelta(&ev)
	if r != "گام اول" {
		t.Fatalf("reasoning = %q", r)
	}
}

func TestExtractQwenSSEError(t *testing.T) {
	raw := `{"error":{"details":"boom happened"}}`
	var ev qwenSSEEvent
	json.Unmarshal([]byte(raw), &ev)
	if msg := extractQwenSSEError(&ev); msg != "boom happened" {
		t.Fatalf("error msg = %q", msg)
	}
	var ev2 qwenSSEEvent
	json.Unmarshal([]byte(`{"choices":[]}`), &ev2)
	if msg := extractQwenSSEError(&ev2); msg != "" {
		t.Fatalf("expected no error, got %q", msg)
	}
}

func TestClassifyQwenAppErrorRateLimited(t *testing.T) {
	err := classifyQwenAppError("RateLimited", "daily cap reached")
	if statusFromError(err.Error()) != 429 {
		t.Fatalf("RateLimited should map to 429, msg=%q", err.Error())
	}
	if !strings.Contains(err.Error(), "RateLimited") {
		t.Fatalf("message should keep upstream detail")
	}
}

func TestClassifyQwenAppErrorUnauthorized(t *testing.T) {
	err := classifyQwenAppError("unauthorized", "session has expired")
	if statusFromError(err.Error()) != 401 {
		t.Fatalf("unauthorized should map to 401, msg=%q", err.Error())
	}
}

func TestIsQwenWAFResponse(t *testing.T) {
	if !isQwenWAFResponse(200, `{"ret":["FAIL_SYS_USER_VALIDATE","RGV587_ERROR::SM"]}`) {
		t.Fatal("RGV587 body should be detected as WAF")
	}
	if !isQwenWAFResponse(504, "anything") {
		t.Fatal("504 should be detected as WAF")
	}
	if isQwenWAFResponse(200, `{"success":true}`) {
		t.Fatal("clean JSON should not be WAF")
	}
}

func TestStatusFromErrorClassifications(t *testing.T) {
	cases := map[string]int{
		"RateLimited: daily cap":            429,
		"unauthorized: session has expired": 401,
		"forbidden: model not allowed":      403,
		"RGV587_ERROR: risk control":        503,
		"upstream HTTP 500: oops":           502,
	}
	for msg, want := range cases {
		if got := statusFromError(msg); got != want {
			t.Errorf("statusFromError(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestDecodeQwenJWT(t *testing.T) {
	// header.payload.signature — payload: {"id":"abc-123","email":"u@x.com"}
	tok := "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6ImFiYy0xMjMiLCJlbWFpbCI6InVAeC5jb20ifQ.sig"
	id, name := decodeJWT(tok)
	if id != "abc-123" {
		t.Fatalf("id = %q", id)
	}
	if name != "u@x.com" {
		t.Fatalf("name = %q (should fall back to email)", name)
	}
}

func TestHumanizeUpstreamErrorPersian(t *testing.T) {
	out := humanizeUpstreamError("RateLimited: you reached the daily cap")
	if !strings.Contains(out, "QWEN_TOKENS") {
		t.Fatalf("RateLimited hint should mention QWEN_TOKENS, got: %s", out)
	}
	out = humanizeUpstreamError("unauthorized: session expired")
	if !strings.Contains(out, "chat.qwen.ai") {
		t.Fatalf("unauthorized hint should point to chat.qwen.ai, got: %s", out)
	}
}

func TestQwenHeadersGuestVsAccount(t *testing.T) {
	acc := qwenHeaders("tok123", "/c/x", false)
	if acc.Get("Cookie") != "token=tok123" {
		t.Fatalf("account headers should carry cookie token, got %q", acc.Get("Cookie"))
	}
	if acc.Get("bx-ua") != "" {
		t.Fatal("account headers must not include baxia headers")
	}
	guest := qwenHeaders("", "/c/x", true)
	if guest.Get("Cookie") != "" {
		t.Fatal("guest headers should have no cookie")
	}
	if guest.Get("bx-ua") == "" || guest.Get("bx-v") != baxiaVersion {
		t.Fatal("guest headers must include baxia tokens")
	}
}

func TestGenerateBaxiaUA(t *testing.T) {
	ua := generateBaxiaUA()
	if !strings.HasPrefix(ua, "2537!") {
		t.Fatalf("bx-ua prefix wrong: %q", ua[:20])
	}
	payload := strings.TrimPrefix(ua, "2537!")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload not base64: %v", err)
	}
	var fp map[string]interface{}
	if json.Unmarshal(decoded, &fp) != nil {
		t.Fatalf("payload not JSON: %s", decoded)
	}
	if _, ok := fp["ts"]; !ok {
		t.Fatal("fingerprint missing ts")
	}
}

func TestLoadBaxiaFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/qwen-bx.json"
	good := `{"bx-ua":"234!captured","bx-umidtoken":"T2gAlive","bx-v":"2.5.37"}`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QWEN_BX_FILE", path)
	got := getBaxiaTokens(false)
	if got.BxUA != "234!captured" || got.BxUmidToken != "T2gAlive" || got.BxV != "2.5.37" {
		t.Fatalf("real bx file not used: %+v", got)
	}

	bad := dir + "/bad.json"
	_ = os.WriteFile(bad, []byte(`{"bx-ua":""}`), 0o600)
	t.Setenv("QWEN_BX_FILE", bad)
	syn := getBaxiaTokens(false)
	if syn.BxUA == "" || syn.BxUmidToken == "" || syn.BxV == "" {
		t.Fatalf("expected synthesized fallback, got %+v", syn)
	}
}
