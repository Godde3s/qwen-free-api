// Entry point of the Qwen bridge (package qbridge).
//
// Run() is what the thin root main.go calls: it parses the CLI flags, wires
// the account pool, serves NewHandler() and blocks until CTRL+C / SIGTERM —
// then drains in-flight requests before exiting.
//
// NewHandler() is exported on its own so integration tests can drive the
// full HTTP surface (all routes + auth + CORS) without starting a listener.

package qbridge

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ============================================================================
// ENTRY POINT
// ============================================================================

// NewHandler assembles the bridge's complete HTTP surface: every route with
// the auth and CORS middleware applied. Used by Run and by the blackbox
// integration tests.
func NewHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", dashboardHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/v1/models", authMiddleware(modelsHandler))
	mux.HandleFunc("/models", authMiddleware(modelsHandler2))
	mux.HandleFunc("/v1/chat/completions", authMiddleware(chatCompletionsHandler))
	mux.HandleFunc("/v1/messages", authMiddleware(anthropicMessagesHandler))
	mux.HandleFunc("/v1/messages/count_tokens", authMiddleware(countTokensHandler))
	mux.HandleFunc("/features", authMiddleware(featuresHandler))
	mux.HandleFunc("/admin/stats", statsHandler)
	mux.HandleFunc("/admin/health", healthHandler)
	mux.HandleFunc("/admin/clients", clientsHandler)
	mux.HandleFunc("/inject.js", injectHandler)
	mux.HandleFunc("/stop", authMiddleware(stopHandler))

	return corsMiddleware(mux)
}

// Run starts the bridge server and blocks until a fatal error or a
// termination signal. Called from the root package's main().
func Run() {
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&config.AgentMode, "agent-mode", config.AgentMode, "Enable agent mode: translate tools & roles for Qwen compatibility (modern shim by default)")
	flag.StringVar(&config.AgentModeVariant, "agent-mode-variant", config.AgentModeVariant, "Agent mode shim variant: modern (default, XML-sectioned prompt) or legacy ([ROLE: ...] rewrite)")
	flag.Parse()

	// ── Multi-account pool (QWEN_TOKENS > QWEN_TOKEN > guest mode) ──────
	initAccounts()

	gRunning.Store(true)

	if config.AgentMode {
		if config.agentModern() {
			logInfo("Agent mode variant: MODERN (XML-sectioned prompt shim, tolerant marker/payload parsing)")
		} else {
			logInfo("Agent mode variant: LEGACY ([ROLE: ...] message rewrite shim)")
		}
	}

	handler := NewHandler()

	addr := fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)

	tokenPadded := fmt.Sprintf("%-44s", config.Auth.Token)
	accountsLine := "guest mode (no tokens — datacenter IPs may hit WAF captcha)"
	if accounts != nil {
		accountsLine = fmt.Sprintf("%d account(s), round-robin + failover", accounts.Len())
	}
	fmt.Printf(`
╔═══════════════════════════════════════════════════════════════╗
║           Qwen Free API Bridge Server Started                 ║
╠═══════════════════════════════════════════════════════════════╣
║  Upstream:      chat.qwen.ai (Qwen3.8-Max & family)           ║
║  Accounts:      %-46s║
║  Dashboard:     http://localhost:%d/                   ║
║  Health:        http://localhost:%d/health               ║
╠═══════════════════════════════════════════════════════════════╣
║  OpenAI API:    http://localhost:%d/v1/chat/completions
║  Anthropic API: http://localhost:%d/v1/messages  ║
╠═══════════════════════════════════════════════════════════════╣
║  Auth Token:    %s║
╚═══════════════════════════════════════════════════════════════╝
`, accountsLine, config.Server.Port, config.Server.Port, config.Server.Port, config.Server.Port, tokenPadded)

	go func() {
		if err := initializeSession(); err != nil {
			log.Println("[Startup] Session init deferred — will retry on first request.")
		}
		// Warm up model cache
		fetchModelsFromQwen()
	}()

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Start serving before blocking on signals.
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	// ── Graceful shutdown ────────────────────────────────────────────────
	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		stopSignal()
		log.Println("[Shutdown] Graceful shutdown requested — draining connections...")

		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := srv.Shutdown(drainCtx); err != nil {
			log.Printf("[Shutdown] drain deadline hit (%v); closing remaining connections", err)
			_ = srv.Close()
		}
		cancel()
		log.Println("[Shutdown] Goodbye.")
	}
}
