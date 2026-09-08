// qwen-login — interactive login helper for Qwen-Free-API.
//
// Opens a real browser window (playwright), you log in to chat.qwen.ai by
// hand (including the slider captcha if Aliyun asks for one), and the tool
// captures the resulting JWT token the moment it appears. Tokens are saved
// to qwen-tokens.json and printed ready-to-paste into QWEN_TOKENS.
//
// Why a helper? The web token is what unlocks the full Qwen3.8-Max family
// through the bridge. Nothing is sent anywhere — the token lands in a local
// file only.
//
// Usage:
//
//      go run ./cmd/qwen-login            # login flow, captures 1 token
//      go run ./cmd/qwen-login --timeout 180
package main

import (
        "encoding/json"
        "flag"
        "fmt"
        "os"
        "strings"
        "time"

        "github.com/mxschmitt/playwright-go"
)

const portalURL = "https://chat.qwen.ai"

type tokenFile struct {
        Tokens []tokenEntry `json:"tokens"`
}

type tokenEntry struct {
        Token     string `json:"token"`
        Label     string `json:"label"`
        AddedUnix int64  `json:"added_unix"`
}

func main() {
        timeout := flag.Int("timeout", 240, "seconds to wait for a login to complete")
        out := flag.String("out", "qwen-tokens.json", "where to append captured tokens")
        headless := flag.Bool("headless", false, "run without a visible browser (cannot solve captcha by hand)")
        flag.Parse()

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
                Args: []string{
                        "--disable-blink-features=AutomationControlled",
                        "--no-sandbox",
                },
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

        fmt.Println("▸ Opening", portalURL)
        fmt.Println("  Log in with your account (email/password or Google/GitHub).")
        fmt.Println("  If a slider captcha appears — drag it. I'm watching for the token…")

        if _, err := page.Goto(portalURL, playwright.PageGotoOptions{
                WaitUntil: playwright.WaitUntilStateDomcontentloaded,
        }); err != nil {
                fmt.Fprintln(os.Stderr, "navigation failed:", err)
                os.Exit(1)
        }

        deadline := time.Now().Add(time.Duration(*timeout) * time.Second)
        var token string
        for time.Now().Before(deadline) {
                // 1) localStorage.token (set right after a successful login)
                if v, err := page.Evaluate("() => localStorage.getItem('token')"); err == nil {
                        if s, _ := v.(string); strings.TrimSpace(s) != "" {
                                token = strings.TrimSpace(s)
                                break
                        }
                }
                // 2) cookie named `token`
                if cookies, err := ctx.Cookies(portalURL); err == nil {
                        for _, c := range cookies {
                                if c.Name == "token" && strings.TrimSpace(c.Value) != "" {
                                        token = strings.TrimSpace(c.Value)
                                        break
                                }
                        }
                }
                if token != "" {
                        break
                }
                time.Sleep(1500 * time.Millisecond)
        }

        if token == "" {
                fmt.Println("✗ Timed out waiting for a token. Try again — complete the login fully.")
                os.Exit(1)
        }

        // Sanity: JWT shape
        if !strings.Contains(token, ".") {
                fmt.Println("✗ Captured value does not look like a JWT — aborting.")
                os.Exit(1)
        }

        entry := tokenEntry{Token: token, Label: "login", AddedUnix: time.Now().Unix()}
        store := tokenFile{}
        if raw, err := os.ReadFile(*out); err == nil {
                _ = json.Unmarshal(raw, &store)
        }
        store.Tokens = append(store.Tokens, entry)
        blob, _ := json.MarshalIndent(store, "", "  ")
        if err := os.WriteFile(*out, blob, 0o600); err != nil {
                fmt.Fprintln(os.Stderr, "save failed:", err)
                os.Exit(1)
        }

        fmt.Println()
        fmt.Println("✓ Token captured and saved to", *out)
        fmt.Println()
        fmt.Println("Put it in .env:")
        fmt.Println()
        fmt.Println("  QWEN_TOKENS=" + token)
        fmt.Println()
        fmt.Println("Then start the bridge:  ./start.sh")
}
