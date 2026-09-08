// Multi-account pool for Qwen (Godde3s edition).
//
// Turns a list of Qwen tokens (QWEN_TOKENS="a,b,c") into a round-robin pool
// with GhostBrain-style resilience:
//
//   - 429 rate limit  -> the account enters an exponential cooldown and the
//     in-flight request transparently fails over to the next healthy account
//     BEFORE any byte of the response has been streamed to the client.
//   - 401/403         -> the account is marked dead (expired/revoked token).
//   - all accounts cooling down -> new requests wait in a bounded queue
//     (ACCOUNT_QUEUE_TIMEOUT seconds, default 120) instead of erroring.
//
// A pool with exactly one account behaves like the classic single-token
// deployment; an EMPTY pool (no QWEN_TOKENS, no QWEN_TOKEN) disables the pool
// entirely and the bridge falls back to the legacy guest-session flow.

package qbridge

import (
    "context"
    "errors"
    "fmt"
    "os"
    "strings"
    "sync"
    "sync/atomic"
    "time"
)

// ============================================================================
// ACCOUNT
// ============================================================================

// Account is one Qwen identity: a JWT token plus its live health state.
type Account struct {
    ID       int
    Token    string
    UserID   string
    UserName string

    mu            sync.Mutex
    cooldownUntil time.Time
    rateLimited   int       // consecutive 429s (reset on success)
    dead          bool      // 401/403: token expired or revoked
    lastError     string
    lastUsed      time.Time

    requests atomic.Int64
    errors   atomic.Int64
    rateHits atomic.Int64
}

func newAccount(id int, token string) *Account {
    a := &Account{ID: id, Token: strings.TrimSpace(token)}
    a.UserID, a.UserName = decodeJWT(a.Token)
    if a.UserName == "" {
        a.UserName = fmt.Sprintf("account-%d", id)
    }
    return a
}

// Label returns a short human-readable identifier for logs.
func (a *Account) Label() string {
    name := a.UserName
    if len(name) > 18 {
        name = name[:18]
    }
    return fmt.Sprintf("#%d(%s)", a.ID, name)
}

// Available reports whether the account can serve a request right now.
func (a *Account) Available() bool {
    a.mu.Lock()
    defer a.mu.Unlock()
    return !a.dead && time.Now().After(a.cooldownUntil)
}

// CooldownLeft returns how long until the account leaves cooldown (0 if now).
func (a *Account) CooldownLeft() time.Duration {
    a.mu.Lock()
    defer a.mu.Unlock()
    d := time.Until(a.cooldownUntil)
    if d < 0 {
        d = 0
    }
    return d
}

// ReportOK marks a successful request: cooldown and 429 streak are reset.
func (a *Account) ReportOK() {
    a.requests.Add(1)
    a.mu.Lock()
    a.rateLimited = 0
    a.lastError = ""
    a.lastUsed = time.Now()
    a.mu.Unlock()
}

// Report429 puts the account into an exponential cooldown:
// base * 2^(streak-1), capped at 30 minutes. base defaults to 60s.
func (a *Account) Report429() {
    a.errors.Add(1)
    a.rateHits.Add(1)
    a.mu.Lock()
    a.rateLimited++
    streak := a.rateLimited
    base := time.Duration(config.AccountCooldownBase) * time.Second
    if base <= 0 {
        base = 60 * time.Second
    }
    d := base
    for i := 1; i < streak && d < 30*time.Minute; i++ {
        d *= 2
    }
    if d > 30*time.Minute {
        d = 30 * time.Minute
    }
    a.cooldownUntil = time.Now().Add(d)
    a.lastError = fmt.Sprintf("rate limited, cooling down %s", d.Truncate(time.Second))
    a.lastUsed = time.Now()
    a.mu.Unlock()
    logAlways(fmt.Sprintf("[Accounts] %s hit 429 — cooldown %s (streak %d)",
        a.Label(), d.Truncate(time.Second), streak))
}

// ReportAuthFail marks the account dead (expired / revoked token).
func (a *Account) ReportAuthFail(reason string) {
    a.errors.Add(1)
    a.mu.Lock()
    if !a.dead {
        a.dead = true
        a.lastError = "auth failed: " + reason
        a.lastUsed = time.Now()
    }
    a.mu.Unlock()
    logAlways(fmt.Sprintf("[Accounts] %s marked DEAD (auth failure): %s", a.Label(), reason))
}

// ReportError records a generic (non-fatal) upstream failure.
func (a *Account) ReportError(err string) {
    a.errors.Add(1)
    a.mu.Lock()
    a.lastError = err
    a.lastUsed = time.Now()
    a.mu.Unlock()
}

// Snapshot is the JSON-friendly status view of one account.
type AccountSnapshot struct {
    ID        int    `json:"id"`
    UserName  string `json:"userName"`
    UserID    string `json:"userId"`
    Healthy   bool   `json:"healthy"`
    Dead      bool   `json:"dead"`
    Cooldown  string `json:"cooldown"`
    Requests  int64  `json:"requests"`
    Errors    int64  `json:"errors"`
    RateHits  int64  `json:"rateLimited"`
    LastError string `json:"lastError,omitempty"`
}

func (a *Account) Snapshot() AccountSnapshot {
    a.mu.Lock()
    defer a.mu.Unlock()
    uid := a.UserID
    if len(uid) > 8 {
        uid = uid[:8] + "..."
    }
    snap := AccountSnapshot{
        ID:        a.ID,
        UserName:  a.UserName,
        UserID:    uid,
        Healthy:   !a.dead && time.Now().After(a.cooldownUntil),
        Dead:      a.dead,
        Requests:  a.requests.Load(),
        Errors:    a.errors.Load(),
        RateHits:  a.rateHits.Load(),
        LastError: a.lastError,
    }
    if d := time.Until(a.cooldownUntil); d > 0 {
        snap.Cooldown = d.Truncate(time.Second).String()
    }
    return snap
}

// ============================================================================
// POOL
// ============================================================================

// AccountPool hands out accounts round-robin, skipping dead / cooling ones.
type AccountPool struct {
    mu       sync.Mutex
    accounts []*Account
    cursor   int
}

// accounts is the process-wide pool; nil means "no pool" (legacy guest flow).
var accounts *AccountPool

// ParseTokensEnv reads QWEN_TOKENS ("a,b,c" — commas, semicolons, spaces and
// newlines are all separators) and returns cleaned, de-duplicated tokens.
func ParseTokensEnv() []string {
    raw := os.Getenv("QWEN_TOKENS")
    if strings.TrimSpace(raw) == "" {
        return nil
    }
    fields := strings.FieldsFunc(raw, func(r rune) bool {
        return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t' || r == '\r'
    })
    seen := map[string]bool{}
    var out []string
    for _, f := range fields {
        f = strings.TrimSpace(f)
        if f == "" || seen[f] {
            continue
        }
        seen[f] = true
        out = append(out, f)
    }
    return out
}

// NewAccountPool builds a pool from tokens. Returns nil for an empty list.
func NewAccountPool(tokens []string) *AccountPool {
    if len(tokens) == 0 {
        return nil
    }
    p := &AccountPool{}
    for i, t := range tokens {
        p.accounts = append(p.accounts, newAccount(i+1, t))
    }
    return p
}

// Len reports the number of accounts in the pool.
func (p *AccountPool) Len() int {
    if p == nil {
        return 0
    }
    p.mu.Lock()
    defer p.mu.Unlock()
    return len(p.accounts)
}

// initAccounts wires the process-wide pool from configuration. Priority:
// QWEN_TOKENS (multi) > QWEN_TOKEN (single) > nil (legacy guest flow).
func initAccounts() {
    toks := ParseTokensEnv()
    if len(toks) == 0 && config.QwenToken != "" {
        toks = []string{config.QwenToken}
    }
    accounts = NewAccountPool(toks)
    if accounts == nil {
        logInfo("[Accounts] no QWEN_TOKENS configured — guest session mode")
        return
    }
    n := accounts.Len()
    healthy := 0
    for _, a := range accounts.accounts {
        if a.Token != "" {
            healthy++
        }
        logInfo(fmt.Sprintf("[Accounts] %s loaded (userID=%s...)",
            a.Label(), shortID(a.UserID)))
    }
    logInfo(fmt.Sprintf("[Accounts] pool ready: %d account(s), round-robin + 429 failover active", healthy))
    _ = n
}

// Pick blocks until an available account shows up (round-robin among the
// healthy set), the request context is done, or the queue timeout expires.
// It is a no-op (nil, nil) when the pool is disabled.
func (p *AccountPool) Pick(ctx context.Context) (*Account, error) {
    if p == nil {
        return nil, nil
    }
    timeout := time.Duration(config.AccountQueueTimeout) * time.Second
    var deadline <-chan time.Time
    if timeout > 0 {
        timer := time.NewTimer(timeout)
        defer timer.Stop()
        deadline = timer.C
    }
    for {
        if acc := p.pickNow(); acc != nil {
            return acc, nil
        }
        // All accounts dead (expired/revoked tokens) never recover — fail
        // fast instead of squatting in the queue until the timeout.
        if p.allDead() {
            return nil, errors.New("all accounts are dead (expired/revoked tokens) — refresh QWEN_TOKENS")
        }
        // everything dead / cooling — wait a beat and retry
        select {
        case <-time.After(250 * time.Millisecond):
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-deadline:
            return nil, errors.New("all accounts are rate-limited or dead; try again shortly or add more QWEN_TOKENS")
        }
    }
}

// allDead reports whether every account is dead (auth failure).
func (p *AccountPool) allDead() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.accounts) == 0 {
		return false
	}
	for _, a := range p.accounts {
		if !a.dead {
			return false
		}
	}
	return true
}

// pickNow returns the next available account in round-robin order, or nil.
func (p *AccountPool) pickNow() *Account {
    p.mu.Lock()
    defer p.mu.Unlock()
    n := len(p.accounts)
    if n == 0 {
        return nil
    }
    for i := 0; i < n; i++ {
        idx := (p.cursor + i) % n
        a := p.accounts[idx]
        if a.Available() {
            p.cursor = (idx + 1) % n
            return a
        }
    }
    return nil
}

// pickOther returns any available account except `exclude` (failover path).
func (p *AccountPool) pickOther(exclude *Account) *Account {
    if p == nil {
        return nil
    }
    p.mu.Lock()
    defer p.mu.Unlock()
    n := len(p.accounts)
    for i := 0; i < n; i++ {
        idx := (p.cursor + i) % n
        a := p.accounts[idx]
        if a != exclude && a.Available() {
            p.cursor = (idx + 1) % n
            return a
        }
    }
    return nil
}

// Report categorizes an upstream error onto the account that served it.
func (p *AccountPool) Report(acc *Account, err error) {
    if acc == nil || err == nil {
        return
    }
    msg := err.Error()
    switch statusFromError(msg) {
    case 429:
        acc.Report429()
    case 401, 403:
        acc.ReportAuthFail(msg)
    default:
        acc.ReportError(msg)
    }
}

// StatusJSON returns the per-account snapshot list (for /status & dashboard).
func (p *AccountPool) StatusJSON() []AccountSnapshot {
    if p == nil {
        return nil
    }
    p.mu.Lock()
    defer p.mu.Unlock()
    out := make([]AccountSnapshot, 0, len(p.accounts))
    for _, a := range p.accounts {
        out = append(out, a.Snapshot())
    }
    return out
}

// HealthyCount reports how many accounts can serve right now.
func (p *AccountPool) HealthyCount() int {
    if p == nil {
        return 0
    }
    p.mu.Lock()
    defer p.mu.Unlock()
    n := 0
    for _, a := range p.accounts {
        if a.Available() {
            n++
        }
    }
    return n
}

// acquireAccountForRequest is the handler-side entry point: it returns the
// account bound to this request (nil = legacy guest mode) or an error when
// the queue gave up waiting.
func acquireAccountForRequest(ctx context.Context) (*Account, error) {
    if accounts == nil {
        return nil, nil
    }
    return accounts.Pick(ctx)
}

// ============================================================================
// CHAT -> ACCOUNT BINDING
// ============================================================================
//
// Throwaway chat sessions are materialized under a specific account when the
// completion is sent. Deletion afterwards must use the SAME token or Qwen
// answers 404 and dead chats accumulate on the account. A tiny binding map
// records which account owns which chat ID between send and cleanup.

var (
    chatBindMu sync.Mutex
    chatBind   = map[string]*Account{} // chatID -> owning account
)

func bindChatAccount(chatID string, a *Account) {
    if chatID == "" || a == nil {
        return
    }
    chatBindMu.Lock()
    chatBind[chatID] = a
    chatBindMu.Unlock()
}

// lookupChatAccount returns the account bound to a chat ID WITHOUT removing
// the binding (deletion may need it twice: token read + init check).
func lookupChatAccount(chatID string) *Account {
    if chatID == "" {
        return nil
    }
    chatBindMu.Lock()
    defer chatBindMu.Unlock()
    return chatBind[chatID]
}

// forgetChatAccount drops the binding once the chat has been finally
// deleted, keeping the map bounded by in-flight requests.
func forgetChatAccount(chatID string) {
    if chatID == "" {
        return
    }
    chatBindMu.Lock()
    delete(chatBind, chatID)
    chatBindMu.Unlock()
}

func shortID(id string) string {
    if len(id) > 8 {
        return id[:8]
    }
    return id
}
