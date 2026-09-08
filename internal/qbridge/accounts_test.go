// Unit tests for the multi-account pool (accounts.go).
//
// Covers:
//   - ParseTokensEnv: separators, dedup, whitespace tolerance
//   - NewAccountPool / round-robin pickNow ordering
//   - Report429 exponential cooldown + Available() gating + pickOther skip
//   - ReportAuthFail marks the account dead (never picked again)
//   - Pick() bounded queue: returns error on ACCOUNT_QUEUE_TIMEOUT expiry
//   - Pool.Report error classification (429 vs 401/403 vs generic)
//   - chat->account binding (bind / lookup / forget)

package qbridge

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestParseTokensEnv(t *testing.T) {
    cases := []struct {
        name string
        env  string
        want []string
    }{
        {"empty", "", nil},
        {"whitespace only", "   \n\t ", nil},
        {"single", "tok1", []string{"tok1"}},
        {"comma", "tok1,tok2,tok3", []string{"tok1", "tok2", "tok3"}},
        {"mixed separators", "tok1; tok2\ntok3\t tok4", []string{"tok1", "tok2", "tok3", "tok4"}},
        {"dedup", "tok1,tok1,tok2, tok1", []string{"tok1", "tok2"}},
        {"trimmed", "  tok1  , tok2  ", []string{"tok1", "tok2"}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            t.Setenv("QWEN_TOKENS", tc.env)
            got := ParseTokensEnv()
            if len(got) != len(tc.want) {
                t.Fatalf("ParseTokensEnv() = %v, want %v", got, tc.want)
            }
            for i := range got {
                if got[i] != tc.want[i] {
                    t.Fatalf("ParseTokensEnv() = %v, want %v", got, tc.want)
                }
            }
        })
    }
}

func TestPoolRoundRobin(t *testing.T) {
    p := NewAccountPool([]string{"tok-a", "tok-b", "tok-c"})
    if p == nil || p.Len() != 3 {
        t.Fatalf("expected 3-account pool")
    }
    order := []int{}
    for i := 0; i < 6; i++ {
        acc, err := p.Pick(context.Background())
        if err != nil || acc == nil {
            t.Fatalf("Pick() = %v, %v", acc, err)
        }
        order = append(order, acc.ID)
    }
    want := []int{1, 2, 3, 1, 2, 3}
    for i := range want {
        if order[i] != want[i] {
            t.Fatalf("round-robin order = %v, want %v", order, want)
        }
    }
}

func TestPoolCooldownOn429(t *testing.T) {
    config.AccountCooldownBase = 60
    p := NewAccountPool([]string{"tok-a", "tok-b"})
    a := p.accounts[0]

    a.Report429()
    if a.Available() {
        t.Fatal("account in cooldown must not be available")
    }
    if a.CooldownLeft() <= 0 || a.CooldownLeft() > time.Minute {
        t.Fatalf("cooldown left = %v, want (0, 60s]", a.CooldownLeft())
    }
    // Next pick must skip to the second account.
    acc, err := p.Pick(context.Background())
    if err != nil || acc == nil || acc.ID != 2 {
        t.Fatalf("Pick() after cooldown = %v, %v — want account 2", acc, err)
    }
    // Second 429 doubles the cooldown.
    a.Report429()
    if d := a.CooldownLeft(); d <= time.Minute {
        t.Fatalf("second 429 should double cooldown, got %v", d)
    }
}

func TestPoolDeadOnAuthFail(t *testing.T) {
    p := NewAccountPool([]string{"tok-a", "tok-b"})
    p.accounts[0].ReportAuthFail("401: token expired")

    if p.accounts[0].Available() {
        t.Fatal("dead account must never be available")
    }
    // Only account 2 remains; every pick returns it.
    for i := 0; i < 3; i++ {
        acc, err := p.Pick(context.Background())
        if err != nil || acc == nil || acc.ID != 2 {
            t.Fatalf("Pick() after auth-fail = %v, %v", acc, err)
        }
    }
    snap := p.accounts[0].Snapshot()
    if !snap.Dead || snap.Healthy {
        t.Fatalf("snapshot should show dead+unhealthy: %+v", snap)
    }
}

func TestPoolQueueTimeout(t *testing.T) {
    old := config.AccountQueueTimeout
    config.AccountQueueTimeout = 1 // second
    defer func() { config.AccountQueueTimeout = old }()

    p := NewAccountPool([]string{"tok-a"})
    p.accounts[0].Report429() // put into 60s cooldown

    start := time.Now()
    _, err := p.Pick(context.Background())
    if err == nil {
        t.Fatal("expected timeout error when all accounts cool down")
    }
    if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
        t.Fatalf("Pick() returned too early (%v) — bounded queue must wait", elapsed)
    }
}

func TestPoolReportClassification(t *testing.T) {
    p := NewAccountPool([]string{"tok-a"})

    p.Report(p.accounts[0], errors.New("Z.AI error 429: rate limited"))
    if p.accounts[0].rateHits.Load() != 1 || p.accounts[0].errors.Load() != 1 {
        t.Fatal("429 should be classified as a rate hit")
    }

    p.Report(p.accounts[0], errors.New("Z.AI error 401: unauthorized"))
    if !p.accounts[0].dead {
        t.Fatal("401 should mark the account dead")
    }
}

func TestPickOtherExcludesCurrent(t *testing.T) {
    p := NewAccountPool([]string{"tok-a", "tok-b"})
    got := p.pickOther(p.accounts[0])
    if got == nil || got.ID != 2 {
        t.Fatalf("pickOther(#1) = %v — want #2", got)
    }
    // Both cooling -> nil.
    p.accounts[1].Report429()
    if p.pickOther(p.accounts[0]) != nil {
        t.Fatal("pickOther must return nil when every other account is cooling")
    }
}

func TestNilPoolIsInert(t *testing.T) {
    var p *AccountPool
    if acc, err := p.Pick(context.Background()); acc != nil || err != nil {
        t.Fatal("nil pool Pick must be (nil, nil)")
    }
    if p.Len() != 0 || p.HealthyCount() != 0 || p.StatusJSON() != nil {
        t.Fatal("nil pool accessors must be inert")
    }
    if got := p.pickOther(nil); got != nil {
        t.Fatal("nil pool pickOther must be nil")
    }
}

func TestChatAccountBinding(t *testing.T) {
    p := NewAccountPool([]string{"tok-a", "tok-b"})

    bindChatAccount("chat-1", p.accounts[0])
    if lookupChatAccount("chat-1") != p.accounts[0] {
        t.Fatal("binding lookup failed")
    }
    // Binding survives double lookup (delete needs token twice).
    if lookupChatAccount("chat-1") != p.accounts[0] {
        t.Fatal("binding must survive repeated lookups")
    }
    forgetChatAccount("chat-1")
    if lookupChatAccount("chat-1") != nil {
        t.Fatal("binding must be gone after forget")
    }
    // Inert paths.
    bindChatAccount("", p.accounts[0])
    bindChatAccount("chat-2", nil)
    forgetChatAccount("")
    if lookupChatAccount("chat-2") != nil {
        t.Fatal("nil binding must not stick")
    }
}
