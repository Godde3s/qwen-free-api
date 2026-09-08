// Configuration for the Qwen bridge (package qbridge).
// .env-first with sane defaults, ported from the GLM-Free-API architecture.

package qbridge

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ============================================================================
// CONFIGURATION
// ============================================================================

const (
	// Qwen web client constants
	DEFAULT_FE_VERSION = "0.2.91"
)

// BASE_URL is a var (not const) only so tests can point the bridge at a
// mock upstream; the default value is the production endpoint.
var BASE_URL = "https://chat.qwen.ai"

// ---------- Config struct (Qwen) ----------

type Config struct {
	Server struct {
		Port int
		Host string
	}
	Auth struct {
		Enabled bool
		Token   string
	}
	Timeouts struct {
		Default int
	}
	QwenToken string
	// QwenTokens holds the multi-account pool (QWEN_TOKENS="tok1,tok2,...").
	// Priority: QWEN_TOKENS > QWEN_TOKEN > guest mode.
	QwenTokens []string
	// AccountQueueTimeout bounds (seconds) how long a request waits in the
	// queue when EVERY account is rate-limited/dead before a 503 is
	// returned (ACCOUNT_QUEUE_TIMEOUT, default 120; 0 waits indefinitely).
	AccountQueueTimeout int
	// AccountCooldownBase is the first 429 cooldown in seconds; subsequent
	// consecutive 429s double it (cap 30m) (ACCOUNT_COOLDOWN_BASE, default 120).
	AccountCooldownBase int
	AgentMode           bool
	// AgentModeVariant selects the agent-mode compatibility shim:
	//   "modern" (default) — XML-sectioned prompt shim (see agent.go)
	//   "legacy"           — the original [ROLE: ...] rewrite shim
	AgentModeVariant string
	Logging          struct {
		Level  string
		Format string
	}
	KnownModels []string
	// StreamHoldback is kept for API compatibility with the GLM edition; the
	// Qwen upstream streams incremental deltas (no edit-based rewrites), so
	// 0 (disable) is the recommended value and the default here.
	StreamHoldback int
}

// loadDotEnv reads a `.env` file from the working directory (if present) and
// injects its KEY=VALUE pairs into the process environment. Existing env vars
// always win, so real environment overrides still work:
//
//	QWEN_TOKENS=token1,token2      # comma/semicolon/space separated
//	AUTH_TOKEN=my-secret
//	AGENT_MODE=1
//
// Comments (#) and blank lines are ignored; values may be single/double
// quoted. This is what makes "copy .env.example to .env, run one file" work.
func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return // no .env — perfectly fine
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ") // tolerate `export KEY=V`
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if i := strings.Index(val, " #"); i >= 0 { // trailing comment
			val = strings.TrimSpace(val[:i])
		}
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if key == "" {
			continue
		}
		if os.Getenv(key) == "" { // real env wins over .env
			_ = os.Setenv(key, val)
		}
	}
}

func loadConfig() *Config {
	loadDotEnv()

	c := &Config{}
	c.Server.Port = 8080
	c.Server.Host = "0.0.0.0"
	c.Auth.Enabled = true
	c.Auth.Token = "qwen"
	c.Timeouts.Default = 300000
	c.QwenToken = ""
	c.AccountQueueTimeout = 120
	c.AccountCooldownBase = 120
	c.AgentMode = false
	c.AgentModeVariant = "modern"
	c.Logging.Level = "debug"
	c.Logging.Format = "text"
	c.KnownModels = []string{"qwen3.8-max", "qwen3.7-plus"}
	c.StreamHoldback = 0

	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			c.Server.Port = n
		}
	}
	if h := os.Getenv("HOST"); h != "" {
		c.Server.Host = h
	}
	if t := os.Getenv("AUTH_TOKEN"); t != "" {
		c.Auth.Token = t
	}
	if t := os.Getenv("TIMEOUT"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			c.Timeouts.Default = n
		}
	}
	if t := os.Getenv("QWEN_TOKEN"); t != "" {
		c.QwenToken = t
	}
	// Multi-account pool: QWEN_TOKENS="token1,token2,token3". Separators:
	// comma / semicolon / whitespace / newline. De-duplicated in ParseTokensEnv.
	if toks := ParseTokensEnv(); len(toks) > 0 {
		c.QwenTokens = toks
	}
	if t := os.Getenv("ACCOUNT_QUEUE_TIMEOUT"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n >= 0 {
			c.AccountQueueTimeout = n
		}
	}
	if t := os.Getenv("ACCOUNT_COOLDOWN_BASE"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			c.AccountCooldownBase = n
		}
	}
	if am := os.Getenv("AGENT_MODE"); am != "" {
		switch strings.ToLower(am) {
		case "1", "true", "yes", "on", "modern":
			c.AgentMode = true
		case "legacy":
			// Explicit opt-in to the old [ROLE: ...] rewrite shim.
			c.AgentMode = true
			c.AgentModeVariant = "legacy"
		case "0", "false", "no", "off":
			c.AgentMode = false
		}
	}
	// AGENT_MODE_VARIANT overrides the shim variant independently of the
	// AGENT_MODE on/off switch: "modern" (default) or "legacy".
	if v := os.Getenv("AGENT_MODE_VARIANT"); v != "" {
		switch strings.ToLower(v) {
		case "legacy":
			c.AgentModeVariant = "legacy"
		case "modern":
			c.AgentModeVariant = "modern"
		}
	}
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		c.Logging.Level = l
	}
	if f := os.Getenv("LOG_FORMAT"); f != "" {
		c.Logging.Format = f
	}
	if h := os.Getenv("STREAM_HOLDBACK"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 0 {
			c.StreamHoldback = n
		}
	}
	return c
}

var config = loadConfig()

// agentModern reports whether the modern agent-mode shim (XML-sectioned
// prompt, tolerant marker/payload parsing — see agent.go) is active.
func (c *Config) agentModern() bool {
	return c.AgentMode && !strings.EqualFold(c.AgentModeVariant, "legacy")
}

// agentLegacy reports whether the legacy agent-mode shim ([ROLE: ...]
// message rewriting — see agent_legacy.go) is active.
func (c *Config) agentLegacy() bool {
	return c.AgentMode && strings.EqualFold(c.AgentModeVariant, "legacy")
}
