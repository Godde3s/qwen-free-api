// Vision placeholder for the Qwen bridge (package qbridge).
//
// The Qwen web chat supports image/document upload through its own file
// API, but that flow needs browser-style multipart uploads per account.
// This build converts image parts into an explicit text note instead of
// silently dropping them, so multimodal agent requests still complete
// (text-only). Native vision upload is planned for a later release.

package qbridge

import (
	"context"
	"encoding/json"
)

// processVisionMessagesAs keeps the GLM-edition handler signature. It scans
// messages for image_url parts, replaces each with a visible text note, and
// returns the cleaned raw JSON plus an empty files list (no upstream upload
// in this build).
func processVisionMessagesAs(ctx context.Context, acc *Account, raw json.RawMessage) (json.RawMessage, []map[string]interface{}, error) {
	_ = ctx
	_ = acc
	var msgs []Message
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return raw, nil, nil // not our shape — pass through untouched
	}
	changed := false
	for i, m := range msgs {
		var parts []map[string]interface{}
		if json.Unmarshal(m.Content, &parts) != nil {
			continue
		}
		out := make([]map[string]interface{}, 0, len(parts))
		for _, p := range parts {
			if t, ok := p["type"].(string); ok && (t == "image_url" || t == "image" || t == "input_image") {
				changed = true
				out = append(out, map[string]interface{}{
					"type": "text",
					"text": "[image attached — vision upload is not supported in this build; describe the image in text if needed]",
				})
				continue
			}
			out = append(out, p)
		}
		if changed {
			if rawOut, err := json.Marshal(out); err == nil {
				msgs[i].Content = rawOut
			}
		}
	}
	if !changed {
		return raw, nil, nil
	}
	rawOut, err := json.Marshal(msgs)
	if err != nil {
		return raw, nil, nil
	}
	return rawOut, nil, nil
}

// extractImageParts finds image parts in raw messages (none in this build —
// images are stripped to text notes by processVisionMessagesAs).
func extractImageParts(raw json.RawMessage) []map[string]interface{} {
	return nil
}

// attachImageParts re-attaches image parts after the agent transform (no-op
// in this build).
func attachImageParts(transformed json.RawMessage, imageParts []map[string]interface{}) ([]byte, error) {
	return []byte(transformed), nil
}
