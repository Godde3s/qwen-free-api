// qwen-bx — one-shot Baxia header harvester for Qwen-Free-API guest mode.
//
// Aliyun's WAF (Baxia) challenges non-browser clients in guest mode. A real
// browser's bx-ua / bx-umidtoken / bx-v header trio passes the WAF even from
// datacenter IPs (verified live). This tool:
//
//  1. opens chat.qwen.ai in a real browser (headless by default),
//  2. captures the bx header trio the page itself sends to /api/v2/,
//  3. verifies it by running a live guest chat in-page,
//  4. saves everything to qwen-bx.json — the bridge picks it up automatically.
//
// Usage:
//
//	go run ./cmd/qwen-bx            # capture + verify + save
//	go run ./cmd/qwen-bx --headless=false
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const portalURL = "https://chat.qwen.ai"

// chatVerifyJS runs a full guest chat in-page and returns the raw result.
const chatVerifyJS = `
async (model) => {
  const ts = Date.now();
  const newRes = await fetch('/api/v2/chats/new', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'source': 'web', 'version': '0.2.91',
              'x-request-id': crypto.randomUUID()},
    body: JSON.stringify({title: 'New Chat', models: [model], chat_mode: 'guest',
                          chat_type: 't2t', timestamp: ts, project_id: ''}),
  });
  const newTxt = await newRes.text();
  let cid = null;
  try { cid = JSON.parse(newTxt).data.id; } catch (e) {}
  if (!cid) return {step: 'chats/new', status: newRes.status, body: newTxt.slice(0, 300)};
  const body = {
    stream: true, incremental_output: true, chat_id: cid, chat_mode: 'guest',
    model: model, parent_id: null,
    messages: [{fid: crypto.randomUUID().replace(/-/g, ''),
                parentId: null, childrenIds: [crypto.randomUUID().replace(/-/g, '')],
                role: 'user', content: 'Say hi in one short sentence.',
                user_action: 'chat', files: [], timestamp: ts, models: [model],
                chat_type: 't2t',
                feature_config: {thinking_enabled: true, output_schema: 'phase',
                                 thinking_mode: 'Auto', thinking_format: 'summary'},
                extra: {meta: {subChatType: 't2t'}}, sub_chat_type: 't2t', parent_id: null}],
    timestamp: ts,
  };
  const res = await fetch('/api/v2/chat/completions?chat_id=' + cid, {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'Accept': 'text/event-stream',
              'source': 'web', 'version': '0.2.91',
              'x-request-id': crypto.randomUUID(), 'x-accel-buffering': 'no'},
    body: JSON.stringify(body),
  });
  const txt = await res.text();
  return {step: 'completions', status: res.status, ct: res.headers.get('content-type'),
          len: txt.length, body: txt.slice(0, 700)};
}
`

type bxFile struct {
	BxUA         string `json:"bx-ua"`
	BxUmidToken  string `json:"bx-umidtoken"`
	BxV          string `json:"bx-v"`
	CapturedUnix int64  `json:"captured_unix,omitempty"`
	Verified     bool   `json:"verified,omitempty"`
}

func main() {
	out := flag.String("out", "qwen-bx.json", "where to save the captured Baxia headers")
	model := flag.String("model", "qwen3.8-max", "model used for the in-page verification chat")
	headless := flag.Bool("headless", true, "run without a visible browser")
	timeout := flag.Int("timeout", 120, "overall timeout in seconds")
	flag.Parse()

	deadline := time.Now().Add(time.Duration(*timeout) * time.Second)

	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); err != nil {
		fmt.Fprintln(os.Stderr, "playwright install failed:", err)
		os.Exit(1)
	}
	pw, err := playwright.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser launch failed:", err)
		os.Exit(1)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(*headless),
		Args:     []string{"--disable-blink-features=AutomationControlled", "--no-sandbox"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "chromium launch failed:", err)
		os.Exit(1)
	}
	defer browser.Close()

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String("Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
			"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{Width: 1280, Height: 850},
		Locale:   playwright.String("en-US"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "context failed:", err)
		os.Exit(1)
	}
	page, err := ctx.NewPage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "page failed:", err)
		os.Exit(1)
	}

	var mu sync.Mutex
	var captured *bxFile
	page.OnRequest(func(req playwright.Request) {
		if !strings.Contains(req.URL(), "/api/v2/") {
			return
		}
		h := req.Headers()
		ua, umid, v := h["bx-ua"], h["bx-umidtoken"], h["bx-v"]
		if ua == "" || umid == "" || v == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if captured == nil {
			captured = &bxFile{BxUA: ua, BxUmidToken: umid, BxV: v}
		}
	})

	fmt.Println("▸ Opening", portalURL)
	if _, err := page.Goto(portalURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "navigation failed:", err)
		os.Exit(1)
	}

	fmt.Println("▸ Waiting for the page to fire its own API calls (Baxia headers)…")
	for captured == nil && time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
	}
	if captured == nil {
		fmt.Println("▸ No bx headers seen yet — poking the API with a guest chats/new…")
		if _, err := page.Evaluate(`
                        async () => {
                                const res = await fetch('/api/v2/chats/new', {
                                        method: 'POST',
                                        headers: {'Content-Type': 'application/json', 'source': 'web',
                                                  'version': '0.2.91', 'x-request-id': crypto.randomUUID()},
                                        body: JSON.stringify({title: 'New Chat', models: ['` + *model + `'],
                                                              chat_mode: 'guest', chat_type: 't2t',
                                                              timestamp: Date.now(), project_id: ''}),
                                });
                                return res.status;
                        }`); err != nil {
			fmt.Fprintln(os.Stderr, "poke failed:", err)
		}
		for captured == nil && time.Now().Before(deadline) {
			time.Sleep(1 * time.Second)
		}
	}
	if captured == nil {
		fmt.Println("✗ Timed out without capturing bx headers. Try --headless=false, or use QWEN_TOKENS instead.")
		os.Exit(1)
	}
	mu.Lock()
	umidPreview := captured.BxUmidToken
	bxV := captured.BxV
	mu.Unlock()
	if len(umidPreview) > 12 {
		umidPreview = umidPreview[:12] + "…"
	}
	fmt.Println("✓ Captured bx-v=" + bxV + "  bx-umidtoken=" + umidPreview)

	// ---- verify the trio with a live in-page guest chat ----
	fmt.Println("▸ Verifying with a live guest chat (" + *model + ")…")
	verified := false
	if res, err := page.Evaluate(chatVerifyJS, *model); err != nil {
		fmt.Fprintln(os.Stderr, "verify evaluate failed:", err)
	} else if m, ok := res.(map[string]any); ok {
		ct, _ := m["ct"].(string)
		body, _ := m["body"].(string)
		step, _ := m["step"].(string)
		low := strings.ToLower(body)
		waf := strings.Contains(low, "rgv587") || strings.Contains(low, "x5secdata") ||
			strings.Contains(low, "_____tmd_____") || strings.Contains(low, "aliyun_waf")
		switch {
		case step == "completions" && strings.HasPrefix(ct, "text/event-stream"):
			fmt.Println("✓ VERIFIED — live guest stream received (" + fmt.Sprint(m["len"]) + " bytes of SSE).")
			verified = true
		case step == "completions" && !waf:
			fmt.Println("✓ WAF passed (app-level response instead of a stream: " + fmt.Sprint(m["status"]) + "). Headers saved.")
			verified = true
		default:
			preview := body
			if len(preview) > 160 {
				preview = preview[:160]
			}
			fmt.Println("⚠ WAF still challenging in-page: " + preview)
		}
	}

	mu.Lock()
	captured.CapturedUnix = time.Now().Unix()
	captured.Verified = verified
	blob, _ := json.MarshalIndent(captured, "", "  ")
	mu.Unlock()
	if err := os.WriteFile(*out, blob, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "save failed:", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("✓ Saved to", *out)
	fmt.Println()
	fmt.Println("Start the bridge normally — it loads this file automatically.")
	fmt.Println("اگر فایل را جای دیگری گذاشتید: QWEN_BX_FILE=" + *out + " را در .env بگذارید.")
	fmt.Println()
	if !verified {
		fmt.Println("Note: verification did not produce a live stream (network/rate-limit).")
		fmt.Println("The headers are still saved — the bridge will fall back to clear errors if they fail.")
	}
}
