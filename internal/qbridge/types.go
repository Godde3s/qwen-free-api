// Core type definitions and global state for the Qwen bridge (package qbridge).

package qbridge

import (
        "encoding/json"
        "regexp"
        "sync"
        "sync/atomic"
        "time"
)

// ============================================================================
// TYPE DEFINITIONS
// ============================================================================

// ---------- Qwen types ----------

type Features struct {
        WebSearch     bool `json:"webSearch"`
        AutoWebSearch bool `json:"autoWebSearch"`
        Thinking      bool `json:"thinking"`
        ImageGen      bool `json:"imageGen"`
        PreviewMode   bool `json:"previewMode"`
}

type Message struct {
        Role    string          `json:"role"`
        Content json.RawMessage `json:"content"`
}

type SessionState struct {
        mu           sync.Mutex
        Token        string
        UserID       string
        UserName     string
        Messages     []Message
        Features     Features
        Initialized  bool
        Initializing bool
}

type UpstreamResult struct {
        Chunk     string
        FullText  string
        Reasoning string
        Err       error
}

type SendOptions struct {
        Model             string
        WebSearch         *bool
        Thinking          *bool
        ImageGen          *bool
        PreviewMode       *bool
        ChatID            string
        Messages          []Message
        ClientMessagesRaw json.RawMessage
        ReasoningEffort   string
        // Account pins this request to one Qwen account (multi-account pool).
        // nil = guest flow (Baxia headers, no cookie).
        Account *Account
}

type ResponseResult struct {
        Content      string
        Text         string
        Prompt       string
        FinishReason string
        Reasoning    string
}

// ============================================================================
// GLOBAL STATE
// ============================================================================

var (
        verbose  bool
        gRunning atomic.Bool
        logMu    sync.Mutex
)

// ---------- Qwen globals ----------

var session = &SessionState{
        UserName: "Guest",
        Features: Features{Thinking: true}, // thinking on by default
}

type ModelInfo struct {
        ID           string
        Name         string
        Description  string
        Capabilities map[string]interface{}
        Created      int64 // upstream "created" unix seconds (0 = unknown)
}

var (
        modelsCache     []ModelInfo
        modelsCacheTime time.Time
        modelsCacheMu   sync.Mutex
)

const modelsCacheTTL = 5 * time.Minute

// Fallback if the Qwen API is unreachable and the cache is empty.
var fallbackModels = []ModelInfo{
        {ID: "qwen3.8-max", Name: "Qwen3.8-Max", Description: "Flagship model — top tier for coding, agents and reasoning (1M context)"},
        {ID: "qwen3.7-plus", Name: "Qwen3.7-Plus", Description: "Fast versatile model with vision and search"},
        {ID: "qwen3.7-max", Name: "Qwen3.7-Max", Description: "Previous generation flagship"},
        {ID: "qwen3.6-plus", Name: "Qwen3.6-Plus", Description: "Balanced multimodal model"},
        {ID: "qwen3.5-plus", Name: "Qwen3.5-Plus", Description: "Classic high-performance model"},
}

var feVersionRe = regexp.MustCompile(`prod-fe-\d+\.\d+\.\d+`)

// ---------- Per-model feature state (dynamic, model-aware) ----------

// ModelFeatureState tracks per-model feature configuration.
// IncludeAll: when true, ALL server capabilities are sent to /completions.
// Overrides: user-supplied per-model feature overrides (snake_case keys).
type ModelFeatureState struct {
        IncludeAll bool
        Overrides  map[string]interface{}
}

var (
        modelFeatureStates   = make(map[string]*ModelFeatureState)
        modelFeatureStatesMu sync.Mutex
)

// normalizeFeatureKey converts a camelCase key to snake_case (see features.go).
