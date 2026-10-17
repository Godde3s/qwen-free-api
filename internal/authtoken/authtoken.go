// Package authtoken resolves the shared secret the router core and the
// embedded bridges use to authenticate against each other.
//
// Historically every bridge defaulted to a hardcoded, publicly known token
// ("Waguri", "qwen", "gemini", ...). That made any install that forgot to set
// AUTH_TOKEN reachable by anyone who had read this repository. Now:
//
//   - AUTH_TOKEN from the environment/.env always wins, and
//   - when it is unset a random per-process token is generated instead.
//
// All packages resolve the token through Get() during package init, so the
// generated value is produced exactly once and every bridge agrees on it.
package authtoken

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strings"
	"sync"
)

var (
	once  sync.Once
	token string
)

// Get returns the process-wide bridge token, generating one if unset.
func Get() string {
	once.Do(func() {
		if v := strings.TrimSpace(os.Getenv("AUTH_TOKEN")); v != "" {
			token = v
			return
		}
		buf := make([]byte, 20) // 40 hex chars — fits the startup banner
		if _, err := rand.Read(buf); err != nil {
			log.Fatalf("[auth] cannot generate AUTH_TOKEN: %v", err)
		}
		token = hex.EncodeToString(buf)
		log.Printf("[auth] AUTH_TOKEN is not set — generated a random one for this run: %s", token)
		log.Printf("[auth] set AUTH_TOKEN in .env to keep the token stable across restarts")
	})
	return token
}
