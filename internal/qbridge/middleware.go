// Code moved from the original main.go monolith during the internal/ restructure.
// See README "Project Structure". Part of the Qwen bridge core (package qbridge).

package qbridge

import (
    "encoding/json"
    "net/http"
    "strings"
)

func toJSON(v interface{}) string {
    b, _ := json.Marshal(v)
    return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}

// ============================================================================
// MIDDLEWARE
// ============================================================================

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Include-All-Features, x-api-key, anthropic-version")
        if r.Method == "OPTIONS" {
            w.WriteHeader(200)
            return
        }
        next.ServeHTTP(w, r)
    })
}

func checkAuth(r *http.Request) bool {
    if !config.Auth.Enabled {
        return true
    }
    authHeader := r.Header.Get("Authorization")
    provided := authHeader
    if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
        provided = authHeader[7:]
    }
    // Anthropic clients send the API key via x-api-key header
    if provided == "" {
        provided = r.Header.Get("x-api-key")
    }
    return provided == config.Auth.Token
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if !config.Auth.Enabled {
            next(w, r)
            return
        }
        if !checkAuth(r) {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(401)
            json.NewEncoder(w).Encode(map[string]interface{}{
                "type": "error",
                "error": map[string]interface{}{
                    "type":    "authentication_error",
                    "message": "Invalid or missing authentication token",
                },
            })
            return
        }
        next(w, r)
    }
}

// ============================================================================
// HTTP HANDLERS
// ============================================================================

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" {
        http.NotFound(w, r)
        return
    }
    serveDashboard(w, r)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
    session.mu.Lock()
    var userIDPreview interface{}
    if session.UserID != "" {
        uid := session.UserID
        if len(uid) > 8 {
            uid = uid[:8]
        }
        userIDPreview = uid + "..."
    }
    features := session.Features
    initialized := session.Initialized
    session.mu.Unlock()

    body := map[string]interface{}{
        "connected":   initialized,
        "userName":    session.UserName,
        "userId":      userIDPreview,
        "features":    features,
        "mode":        "guest",
    }
    if accounts != nil {
        body["mode"] = "accounts"
        body["accountPool"] = map[string]interface{}{
            "enabled": true,
            "size":    accounts.Len(),
            "healthy": accounts.HealthyCount(),
        }
        body["accounts"] = accounts.StatusJSON()
    } else {
        body["accountPool"] = map[string]interface{}{
            "enabled": false,
            "size":    0,
            "healthy": 0,
        }
    }

    writeJSON(w, 200, body)
}

