// Qwen Free API Bridge — chat.qwen.ai → OpenAI/Anthropic compatible proxy.
//
// Thin entry point: the whole bridge lives in internal/qbridge (see README
// "Project Structure"), mirroring the GLM-Free-API layout this project
// follows. Token login is a separate helper under cmd/qwen-login.
//
// Build:  go build -trimpath -ldflags="-s -w" -o qwen-api .
// Run:    ./qwen-api            (or: go run .)

package main

import "github.com/Godde3s/qwen-free-api/internal/qbridge"

func main() {
	qbridge.Run()
}
