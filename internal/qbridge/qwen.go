// Qwen upstream client — talks directly to chat.qwen.ai (Open WebUI-style
// v2 API). Ported from the GLM-Free-API architecture (Godde3s edition).
//
// Request flow (mirrors what the official web client does):
//
//      1. POST /api/v2/chats/new                      -> chat_id
//      2. POST /api/v2/chat/completions?chat_id=...   -> SSE stream
//
// Two identity modes:
//
//   - ACCOUNT mode (primary): every request carries `Cookie: token=<JWT>`.
//     Proven to pass the Aliyun WAF cleanly from datacenter IPs; errors come
//     back as clean application-level JSON (RateLimited / unauthorized).
//   - GUEST mode (fallback): no cookie + synthesized Baxia headers
//     (bx-ua fingerprint + real umidtoken from Alibaba's public wu.json).
//     Works from residential IPs; datacenter IPs usually get an RGV587
//     captcha challenge, which is surfaced as a human-readable error.
//
// Upstream always streams (stream:false is not supported by the web API);
// non-stream client requests are accumulated internally.

package qbridge

import (
        "bufio"
        "bytes"
        "context"
        "crypto/md5"
        "crypto/rand"
        "encoding/base64"
        "encoding/json"
        "errors"
        "fmt"
        "io"
        "log"
        "math/big"
        "net/http"
        "regexp"
        "strings"
        "sync"
        "time"
)

// upstreamErr carries an HTTP-ish status so statusFromError can classify it
// for the account pool (429 cooldown / 401 dead-marking).
type upstreamErr struct {
        status int
        msg    string
}

func (e *upstreamErr) Error() string { return e.msg }

const (
        qwenWebVersion = "0.2.91"
        qwenUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
                "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
        baxiaVersion = "2.5.36"
        wumURL       = "https://sg-wum.alibaba.com/w/wu.json"
)


// ============================================================================
// BAXIA (Aliyun WAF) — guest-mode anti-bot headers
// ============================================================================

var baxiaRenderers = []string{
        "ANGLE (Intel, Intel(R) UHD Graphics 630, OpenGL 4.6)",
        "ANGLE (NVIDIA, NVIDIA GeForce GTX 1080, OpenGL 4.6)",
        "ANGLE (AMD, AMD Radeon RX 580, OpenGL 4.6)",
        "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060, OpenGL 4.6)",
}

var baxiaPlatforms = []string{"Win32", "Linux x86_64", "MacIntel"}

type baxiaTokens struct {
        BxUA        string
        BxUmidToken string
        BxV         string
}

var (
        baxiaMu       sync.Mutex
        baxiaCache    *baxiaTokens
        baxiaCacheAt  time.Time
        baxiaCacheTTL = 4 * time.Minute
)

func baxiaRandomString(n int) string {
        const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/"
        b := make([]byte, n)
        for i := range b {
                idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
                b[i] = chars[idx.Int64()]
        }
        return string(b)
}

func baxiaMD5B64(s string) string {
        sum := md5.Sum([]byte(s))
        out := base64.StdEncoding.EncodeToString(sum[:])
        if len(out) > 32 {
                out = out[:32]
        }
        return out
}

// randIntn returns a cryptographically-seeded random int in [0,n).
func randIntn(n int) int {
        if n <= 0 {
                return 0
        }
        idx, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
        if err != nil {
                return 0
        }
        return int(idx.Int64())
}

// randFloat64 returns a random float in [0,1).
func randFloat64() float64 {
        idx, err := rand.Int(rand.Reader, big.NewInt(1<<52))
        if err != nil {
                return 0
        }
        return float64(idx.Int64()) / (1 << 52)
}

func generateBaxiaUA() string {
        fp := map[string]interface{}{
                "p":  baxiaPlatforms[randIntn(len(baxiaPlatforms))],
                "l":  []string{"en-US", "zh-CN", "en-GB"}[randIntn(3)],
                "hc": 4 + randIntn(12),
                "dm": []int{4, 8, 16, 32}[randIntn(4)],
                "to": []int{-480, -300, 0, 60, 480}[randIntn(5)],
                "sw": 1920 + randIntn(200),
                "sh": 1080 + randIntn(100),
                "cd": 24,
                "pr": []float64{1, 1.25, 1.5, 2}[randIntn(4)],
                "wf": baxiaRenderers[randIntn(len(baxiaRenderers))][:20],
                "cf": baxiaMD5B64(baxiaRandomString(32)),
                "af": fmt.Sprintf("%.14f", 124.04347527516074+randFloat64()*0.001),
                "ts": time.Now().UnixMilli(),
                "r":  randFloat64(),
        }
        raw, _ := json.Marshal(fp)
        return strings.ReplaceAll(baxiaVersion, ".", "") + "!" +
                base64.StdEncoding.EncodeToString(raw)
}

func fetchUmidToken() string {
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        req, err := http.NewRequestWithContext(ctx, "GET", wumURL, nil)
        if err != nil {
                return "T2gA" + baxiaRandomString(40)
        }
        req.Header.Set("User-Agent", qwenUserAgent)
        resp, err := upstreamClient.Do(req)
        if err != nil {
                return "T2gA" + baxiaRandomString(40)
        }
        defer resp.Body.Close()
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
        re := regexp.MustCompile(`umx\.wu\('([^']+)'\)`)
        if m := re.FindSubmatch(body); m != nil && strings.HasPrefix(string(m[1]), "T2gA") {
                return string(m[1])
        }
        re2 := regexp.MustCompile(`'(T2gA[^']+)'`)
        if m := re2.FindSubmatch(body); m != nil {
                return string(m[1])
        }
        if etag := resp.Header.Get("ETag"); strings.HasPrefix(etag, "T2gA") {
                return strings.Trim(etag, `"`)
        }
        return "T2gA" + baxiaRandomString(40)
}

func getBaxiaTokens(force bool) baxiaTokens {
        baxiaMu.Lock()
        defer baxiaMu.Unlock()
        if !force && baxiaCache != nil && time.Since(baxiaCacheAt) < baxiaCacheTTL {
                return *baxiaCache
        }
        t := baxiaTokens{
                BxUA:        generateBaxiaUA(),
                BxUmidToken: fetchUmidToken(),
                BxV:         baxiaVersion,
        }
        baxiaCache = &t
        baxiaCacheAt = time.Now()
        return t
}

// ============================================================================
// HEADERS
// ============================================================================

func qwenHeaders(token string, refererPath string, guest bool) http.Header {
        h := http.Header{}
        h.Set("Accept", "application/json, text/event-stream")
        h.Set("Content-Type", "application/json")
        h.Set("Origin", BASE_URL)
        h.Set("Referer", BASE_URL+refererPath)
        h.Set("source", "web")
        h.Set("version", qwenWebVersion)
        h.Set("User-Agent", qwenUserAgent)
        h.Set("Accept-Language", "en-US,en;q=0.9")
        h.Set("x-request-id", randomUUID())
        h.Set("x-accel-buffering", "no")
        if guest && token == "" {
                bx := getBaxiaTokens(false)
                h.Set("bx-ua", bx.BxUA)
                h.Set("bx-umidtoken", bx.BxUmidToken)
                h.Set("bx-v", bx.BxV)
        }
        if token != "" {
                h.Set("Cookie", "token="+token)
        }
        return h
}

// ============================================================================
// JWT (token identity)
// ============================================================================

func decodeJWT(token string) (id, name string) {
        parts := strings.Split(token, ".")
        if len(parts) < 2 {
                return "", ""
        }
        payload, err := base64.RawURLEncoding.DecodeString(parts[1])
        if err != nil {
                // tolerate standard padding too
                payload, err = base64.StdEncoding.DecodeString(parts[1])
                if err != nil {
                        return "", ""
                }
        }
        var claims struct {
                ID    string `json:"id"`
                Email string `json:"email"`
                Name  string `json:"name"`
        }
        if json.Unmarshal(payload, &claims) != nil {
                return "", ""
        }
        name = claims.Name
        if name == "" {
                name = claims.Email
        }
        return claims.ID, name
}

// ============================================================================
// CHAT SESSIONS (stateless throwaway chats)
// ============================================================================

// createQwenChat mints a fresh chat session and returns its ID.
func createQwenChat(model string, acc *Account, guest bool) (string, error) {
        token := ""
        if acc != nil {
                token = acc.Token
        }
        mode := "normal"
        if guest || token == "" {
                mode = "guest"
        }
        var lastErr error
        for attempt := 0; attempt < 3; attempt++ {
                if attempt > 0 && guest {
                        getBaxiaTokens(true) // fresh fingerprint on retry
                }
                body := map[string]interface{}{
                        "title":      "New Chat",
                        "models":     []string{model},
                        "chat_mode":  mode,
                        "chat_type":  "t2t",
                        "timestamp":  time.Now().UnixMilli(),
                        "project_id": "",
                }
                raw, _ := json.Marshal(body)
                ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
                req, err := http.NewRequestWithContext(ctx, "POST",
                        BASE_URL+"/api/v2/chats/new", bytes.NewReader(raw))
                if err != nil {
                        cancel()
                        return "", &upstreamErr{502, "upstream request build failed: " + err.Error()}
                }
                req.Header = qwenHeaders(token, "/c/new-chat", guest)
                resp, err := upstreamClient.Do(req)
                if err != nil {
                        cancel()
                        lastErr = &upstreamErr{502, "chats/new network error: " + err.Error()}
                        time.Sleep(time.Duration(attempt+1) * time.Second)
                        continue
                }
                respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
                resp.Body.Close()
                cancel()
                text := string(respBody)
                if resp.StatusCode >= 400 || isQwenWAFResponse(resp.StatusCode, text) {
                        lastErr = classifyQwenFail(resp.StatusCode, text)
                        time.Sleep(time.Duration(attempt+1) * time.Second)
                        continue
                }
                var parsed struct {
                        Success bool `json:"success"`
                        Data    struct {
                                ID  string `json:"id"`
                                Msg string `json:"message"`
                                Code string `json:"code"`
                        } `json:"data"`
                }
                if err := json.Unmarshal(respBody, &parsed); err != nil {
                        lastErr = &upstreamErr{502, "chats/new returned non-JSON response (WAF?)"}
                        time.Sleep(time.Duration(attempt+1) * time.Second)
                        continue
                }
                if !parsed.Success || parsed.Data.ID == "" {
                        code := parsed.Data.Code
                        msg := parsed.Data.Msg
                        if msg == "" {
                                msg = code
                        }
                        if msg == "" {
                                msg = "chats/new returned no chat id"
                        }
                        lastErr = classifyQwenAppError(code, msg)
                        time.Sleep(time.Duration(attempt+1) * time.Second)
                        continue
                }
                return parsed.Data.ID, nil
        }
        return "", lastErr
}

// deleteQwenChat best-effort removes a throwaway chat so accounts don't
// accumulate dead sessions. Failures are silently ignored.
func deleteQwenChat(chatID string, acc *Account) {
        if chatID == "" || acc == nil || acc.Token == "" {
                return
        }
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        req, err := http.NewRequestWithContext(ctx, "DELETE",
                BASE_URL+"/api/v2/chats?ids="+chatID, nil)
        if err != nil {
                return
        }
        req.Header = qwenHeaders(acc.Token, "/c/"+chatID, false)
        if resp, err := upstreamClient.Do(req); err == nil {
                resp.Body.Close()
        }
}

// Stateless session contract (handlers.go): Acquire returns a chat handle,
// Release cleans it up. For Qwen the chat is minted inside the completion
// call (it needs the model anyway), so Acquire hands out a pre-reserved ID
// placeholder and the real chat is bound during send. We keep the same
// function names so the handler flow stays identical to GLM-Free-API.
//
//      AcquireStatelessSession(ctx) -> ("pending", false, nil)
//      ReleaseStatelessSession(id, pooled) -> best-effort upstream delete

var (
        statelessMu     sync.Mutex
        statelessChats  = map[string]*Account{} // placeholder id -> serving account
        statelessRealID = map[string]string{}   // placeholder -> real qwen chat id
)

func AcquireStatelessSession(ctx context.Context) (string, bool, error) {
        if err := ctx.Err(); err != nil {
                return "", false, err
        }
        id := "q-" + randomUUID()
        statelessMu.Lock()
        statelessChats[id] = nil
        statelessMu.Unlock()
        return id, false, nil
}

func bindStatelessChat(placeholder, realID string, acc *Account) {
        if placeholder == "" {
                return
        }
        statelessMu.Lock()
        statelessChats[placeholder] = acc
        if realID != "" {
                statelessRealID[placeholder] = realID
        }
        statelessMu.Unlock()
}

func ReleaseStatelessSession(placeholder string, pooled bool) {
        statelessMu.Lock()
        acc := statelessChats[placeholder]
        realID := statelessRealID[placeholder]
        delete(statelessChats, placeholder)
        delete(statelessRealID, placeholder)
        statelessMu.Unlock()
        if realID != "" {
                go deleteQwenChat(realID, acc)
        }
}

// ============================================================================
// COMPLETION BODY
// ============================================================================

func buildQwenBody(chatID, model, prompt string, thinking bool) map[string]interface{} {
        featureConfig := map[string]interface{}{
                "thinking_enabled":  thinking,
                "output_schema":     "phase",
                "research_mode":     "normal",
                "auto_thinking":     thinking,
                "thinking_mode":     map[bool]string{true: "Auto", false: "Disabled"}[thinking],
                "thinking_format":   "summary",
                "auto_search":       false,
                "code_interpreter":  false,
                "plugins_enabled":   false,
                "function_calling":  false,
                "enable_tools":      false,
                "enable_function_call": false,
                "tool_choice":       "none",
        }
        if search, ok := resolveFeaturesForModel(model)["auto_web_search"].(bool); ok && search {
                featureConfig["auto_search"] = true
        }
        ts := time.Now().UnixMilli()
        fid := randomUUID()
        return map[string]interface{}{
                "stream":             true,
                "incremental_output": true,
                "chat_id":            chatID,
                "chat_mode":          "normal",
                "model":              model,
                "parent_id":          nil,
                "messages": []map[string]interface{}{{
                        "fid":            fid,
                        "parentId":       nil,
                        "childrenIds":    []string{randomUUID()},
                        "role":           "user",
                        "content":        prompt,
                        "user_action":    "chat",
                        "files":          []interface{}{},
                        "timestamp":      ts,
                        "models":         []string{model},
                        "chat_type":      "t2t",
                        "feature_config": featureConfig,
                        "extra":          map[string]interface{}{"meta": map[string]string{"subChatType": "t2t"}},
                        "sub_chat_type":  "t2t",
                        "parent_id":      nil,
                }},
                "timestamp": ts,
        }
}

func buildQwenGuestBody(chatID, model, prompt string, thinking bool) map[string]interface{} {
        body := buildQwenBody(chatID, model, prompt, thinking)
        body["chat_mode"] = "guest"
        return body
}

// ============================================================================
// SSE PARSING
// ============================================================================

type qwenSSEEvent struct {
        Choices []struct {
                Delta struct {
                        Content          string `json:"content"`
                        ReasoningContent string `json:"reasoning_content"`
                        Reasoning        string `json:"reasoning"`
                        Phase            string `json:"phase"`
                        Extra            struct {
                                SummaryThought struct {
                                        Content json.RawMessage `json:"content"`
                                } `json:"summary_thought"`
                        } `json:"extra"`
                } `json:"delta"`
                FinishReason string `json:"finish_reason"`
        } `json:"choices"`
        Content string `json:"content"`
        Error   json.RawMessage `json:"error"`
        Usage   *struct {
                InputTokens  int `json:"input_tokens"`
                OutputTokens int `json:"output_tokens"`
                TotalTokens  int `json:"total_tokens"`
        } `json:"usage"`
}

// extractQwenDelta returns (contentDelta, reasoningDelta, finishReason).
func extractQwenDelta(ev *qwenSSEEvent) (string, string, string) {
        if len(ev.Choices) == 0 {
                return ev.Content, "", ""
        }
        d := ev.Choices[0].Delta
        reasoning := d.ReasoningContent
        if reasoning == "" {
                reasoning = d.Reasoning
        }
        if reasoning == "" && d.Phase == "thinking_summary" && len(d.Extra.SummaryThought.Content) > 0 {
                var sc string
                if json.Unmarshal(d.Extra.SummaryThought.Content, &sc) == nil {
                        reasoning = sc
                } else {
                        var arr []string
                        if json.Unmarshal(d.Extra.SummaryThought.Content, &arr) == nil {
                                reasoning = strings.Join(arr, "")
                        }
                }
        }
        return d.Content, reasoning, ev.Choices[0].FinishReason
}

func extractQwenSSEError(ev *qwenSSEEvent) string {
        if len(ev.Error) == 0 {
                return ""
        }
        var obj struct {
                Details string `json:"details"`
                Message string `json:"message"`
                Code    string `json:"code"`
        }
        if json.Unmarshal(ev.Error, &obj) == nil {
                if obj.Details != "" {
                        return obj.Details
                }
                if obj.Message != "" {
                        return obj.Message
                }
                if obj.Code != "" {
                        return obj.Code
                }
        }
        return string(ev.Error)
}

// ============================================================================
// ERROR CLASSIFICATION
// ============================================================================

var rgv587Re = regexp.MustCompile(`(?i)rgv587|punish|action=deny|pureDenyWait|_____tmd_____|x5secdata|FAIL_SYS_USER_VALIDATE`)
var aliyunWAFRe = regexp.MustCompile(`(?i)aliyun_waf|baxia`)

func isQwenWAFResponse(status int, body string) bool {
        if status == 504 {
                return true
        }
        return rgv587Re.MatchString(body) || aliyunWAFRe.MatchString(body)
}

// classifyQwenFail maps an HTTP-level failure to a typed error.
func classifyQwenFail(status int, body string) error {
        if isQwenWAFResponse(status, body) {
                return &upstreamErr{503, "RGV587_ERROR: Aliyun risk-control challenged the request (WAF captcha). " +
                        "Guest mode from this network is blocked — add account tokens via QWEN_TOKENS, or run the qwen-login helper."}
        }
        switch {
        case status == 401:
                return &upstreamErr{401, "unauthorized: token expired or invalid — re-login at chat.qwen.ai and refresh QWEN_TOKENS"}
        case status == 403:
                return &upstreamErr{403, "forbidden: account blocked or model not allowed for this account"}
        case status == 429:
                return &upstreamErr{429, "rate limited by upstream — account entered cooldown"}
        default:
                preview := body
                if len(preview) > 200 {
                        preview = preview[:200]
                }
                return &upstreamErr{502, fmt.Sprintf("upstream HTTP %d: %s", status, preview)}
        }
}

// classifyQwenAppError maps an application-level {success:false,...} error.
func classifyQwenAppError(code, msg string) error {
        switch strings.ToLower(code) {
        case "ratelimited":
                return &upstreamErr{429, "RateLimited: " + msg + " — this account hit its daily usage cap; add another QWEN_TOKENS entry or wait for the reset"}
        case "unauthorized":
                return &upstreamErr{401, "unauthorized: " + msg + " — refresh the token (F12 → Application → Cookies → token)"}
        case "forbidden":
                return &upstreamErr{403, "forbidden: " + msg}
        default:
                if code != "" {
                        return &upstreamErr{502, code + ": " + msg}
                }
                return &upstreamErr{502, msg}
        }
}

// statusFromError classifies an error message for the account pool.
func statusFromError(errMsg string) int {
        if errMsg == "" {
                return 500
        }
        l := strings.ToLower(errMsg)
        switch {
        case strings.Contains(l, "ratelimited"), strings.Contains(l, "rate limited"), strings.Contains(l, "429"):
                return 429
        case strings.Contains(l, "unauthorized"), strings.Contains(l, "session has expired"), strings.Contains(l, "401"):
                return 401
        case strings.Contains(l, "forbidden"), strings.Contains(l, "403"):
                return 403
        case rgv587Re.MatchString(errMsg), aliyunWAFRe.MatchString(errMsg), strings.Contains(l, "waf"):
                return 503
        default:
                return 502
        }
}

// humanizeUpstreamError turns raw upstream errors into actionable messages.
func humanizeUpstreamError(detail string) string {
        l := strings.ToLower(detail)
        switch {
        case strings.Contains(l, "ratelimited"):
                return "سقف مصرف روزانه این اکانت پر شده — یک توکن دیگر به QWEN_TOKENS اضافه کنید یا صبح فردا دوباره تلاش کنید (ریست روزانه). | " + detail
        case strings.Contains(l, "unauthorized"), strings.Contains(l, "session has expired"):
                return "توکن منقضی یا نامعتبر است — وارد chat.qwen.ai شوید و توکن جدید را در QWEN_TOKENS بگذارید (F12 → Application → Cookies → token). | " + detail
        case rgv587Re.MatchString(detail):
                return "ریسک‌کنترل علی‌بابا (کپچا) درخواست مهمان را بلاک کرد — با توکن اکانت (QWEN_TOKENS) درخواست بدهید یا از شبکه خانگی استفاده کنید. | " + detail
        case strings.Contains(l, "forbidden"):
                return "این مدل برای سطح اکانت شما باز نیست — مدل دیگری انتخاب کنید یا qwen3.7-plus را امتحان کنید. | " + detail
        default:
                return detail
        }
}

// extractUpstreamError pulls a human-readable message out of a JSON error body.
func extractUpstreamError(j map[string]interface{}) string {
        if data, ok := j["data"].(map[string]interface{}); ok {
                if d, ok := data["details"].(string); ok && d != "" {
                        return d
                }
                if c, ok := data["code"].(string); ok && c != "" {
                        return c
                }
        }
        if e, ok := j["error"].(map[string]interface{}); ok {
                if m, ok := e["message"].(string); ok && m != "" {
                        return m
                }
        }
        if m, ok := j["message"].(string); ok && m != "" {
                return m
        }
        raw, _ := json.Marshal(j)
        if len(raw) > 300 {
                raw = raw[:300]
        }
        return string(raw)
}

func isRetryableUpstreamError(err error) bool {
        if err == nil {
                return false
        }
        return statusFromError(err.Error()) >= 500 || statusFromError(err.Error()) == 429
}

// ============================================================================
// SESSION WARMUP (light — no credentials needed)
// ============================================================================

func initializeSession() error {
        session.mu.Lock()
        session.Initialized = true
        session.mu.Unlock()
        return nil
}

// ============================================================================
// COMPLETION (single attempt)
// ============================================================================

func sendUpstream(prompt string, opts SendOptions) (<-chan UpstreamResult, error) {
        out := make(chan UpstreamResult, 64)

        guest := opts.Account == nil || opts.Account.Token == ""
        token := ""
        if opts.Account != nil {
                token = opts.Account.Token
        }
        model := opts.Model
        thinking := true
        if opts.Thinking != nil {
                thinking = *opts.Thinking
        } else if v, ok := resolveFeaturesForModel(model)["enable_thinking"].(bool); ok {
                thinking = v
        }

        chatID, err := createQwenChat(model, opts.Account, guest)
        if err != nil {
                return nil, err
        }
        if opts.ChatID != "" {
                bindStatelessChat(opts.ChatID, chatID, opts.Account)
        }

        body := buildQwenBody(chatID, model, prompt, thinking)
        if guest {
                body = buildQwenGuestBody(chatID, model, prompt, thinking)
        }
        raw, _ := json.Marshal(body)

        ctx, cancel := context.WithCancel(context.Background())
        req, err := http.NewRequestWithContext(ctx, "POST",
                BASE_URL+"/api/v2/chat/completions?chat_id="+chatID, bytes.NewReader(raw))
        if err != nil {
                cancel()
                return nil, &upstreamErr{502, "completion request build failed: " + err.Error()}
        }
        req.Header = qwenHeaders(token, "/c/"+chatID, guest)

        resp, err := upstreamClient.Do(req)
        if err != nil {
                cancel()
                return nil, &upstreamErr{502, "completion network error: " + err.Error()}
        }

        if resp.StatusCode >= 400 || resp.Header.Get("Content-Type") == "" {
                respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
                resp.Body.Close()
                cancel()
                ct := resp.Header.Get("Content-Type")
                if !strings.Contains(ct, "json") {
                        return nil, classifyQwenFail(resp.StatusCode, string(respBody))
                }
                var j map[string]interface{}
                if json.Unmarshal(respBody, &j) == nil {
                        return nil, classifyQwenAppError(
                                stringFromMap(j["data"], "code"), extractUpstreamError(j))
                }
                return nil, classifyQwenFail(resp.StatusCode, string(respBody))
        }

        ct := resp.Header.Get("Content-Type")
        if !strings.Contains(ct, "event-stream") {
                // Some errors arrive as 200 + application/json {success:false}
                respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
                resp.Body.Close()
                cancel()
                var j map[string]interface{}
                if json.Unmarshal(respBody, &j) == nil {
                        if success, ok := j["success"].(bool); ok && !success {
                                dataCode := stringFromMap(j["data"], "code")
                                dataMsg := extractUpstreamError(j)
                                return nil, classifyQwenAppError(dataCode, dataMsg)
                        }
                }
                if isQwenWAFResponse(resp.StatusCode, string(respBody)) {
                        return nil, classifyQwenFail(resp.StatusCode, string(respBody))
                }
                return nil, &upstreamErr{502, "upstream returned non-SSE response (content-type: " + ct + ")"}
        }

        go func() {
                defer close(out)
                defer resp.Body.Close()
                defer cancel()
                scanner := bufio.NewScanner(resp.Body)
                scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
                sawFinish := false
                for scanner.Scan() {
                        line := strings.TrimSpace(scanner.Text())
                        if !strings.HasPrefix(line, "data:") {
                                continue
                        }
                        payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
                        if payload == "" || payload == "[DONE]" {
                                if payload == "[DONE]" {
                                        break
                                }
                                continue
                        }
                        var ev qwenSSEEvent
                        if json.Unmarshal([]byte(payload), &ev) != nil {
                                continue
                        }
                        if errMsg := extractQwenSSEError(&ev); errMsg != "" {
                                select {
                                case out <- UpstreamResult{Err: &upstreamErr{statusFromError(errMsg), humanizeUpstreamError(errMsg)}}:
                                case <-ctx.Done():
                                }
                                return
                        }
                        content, reasoning, finish := extractQwenDelta(&ev)
                        if reasoning != "" {
                                out <- UpstreamResult{Reasoning: reasoning}
                        }
                        if content != "" {
                                out <- UpstreamResult{Chunk: content}
                        }
                        if finish != "" {
                                sawFinish = true
                                break
                        }
                }
                if err := scanner.Err(); err != nil && !sawFinish {
                        select {
                        case out <- UpstreamResult{Err: &upstreamErr{502, "upstream stream error: " + err.Error()}}:
                        default:
                        }
                }
        }()
        return out, nil
}

// sendUpstreamWithFailover tries accounts round-robin until one produces a
// stream. Failover only happens BEFORE the first byte reaches the client.
func sendUpstreamWithFailover(prompt string, opts SendOptions) (<-chan UpstreamResult, error) {
        acc := opts.Account
        maxTries := 1
        if accounts != nil {
                maxTries = accounts.Len()
                if maxTries > 4 {
                        maxTries = 4
                }
        }
        var lastErr error
        for try := 0; try < maxTries; try++ {
                if try > 0 {
                        if accounts == nil {
                                break
                        }
                        next := accounts.pickOther(acc)
                        if next == nil {
                                break
                        }
                        acc = next
                        opts.Account = acc
                        log.Printf("[Failover] retrying with %s (attempt %d/%d)", acc.Label(), try+1, maxTries)
                }
                ch, err := sendUpstream(prompt, opts)
                if err != nil {
                        lastErr = err
                        if accounts != nil {
                                accounts.Report(acc, err)
                        }
                        if isRetryableUpstreamError(err) {
                                continue
                        }
                        return nil, errors.New(humanizeUpstreamError(err.Error()))
                }
                // Peek the first event: fail fast on pre-stream errors.
                first, ok := <-ch
                if !ok {
                        lastErr = &upstreamErr{502, "upstream closed the stream before sending any data"}
                        if accounts != nil {
                                accounts.Report(acc, lastErr)
                        }
                        continue
                }
                if first.Err != nil {
                        lastErr = first.Err
                        if accounts != nil {
                                accounts.Report(acc, first.Err)
                        }
                        if isRetryableUpstreamError(first.Err) {
                                continue
                        }
                        return nil, errors.New(first.Err.Error())
                }
                if acc != nil {
                        acc.ReportOK()
                }
                return refeedChannel(ch, first), nil
        }
        if lastErr != nil {
                return nil, errors.New(humanizeUpstreamError(lastErr.Error()))
        }
        return nil, &upstreamErr{503, "no account available — add tokens via QWEN_TOKENS or configure guest mode"}
}

// refeedChannel puts the already-consumed first event back on top.
func refeedChannel(ch <-chan UpstreamResult, first UpstreamResult) <-chan UpstreamResult {
        out := make(chan UpstreamResult, cap(ch)+1)
        out <- first
        go func() {
                defer close(out)
                for v := range ch {
                        out <- v
                }
        }()
        return out
}

// stringFromMap is a tiny helper for typed map access.
func stringFromMap(v interface{}, key string) string {
        if m, ok := v.(map[string]interface{}); ok {
                s, _ := m[key].(string)
                return s
        }
        return ""
}
