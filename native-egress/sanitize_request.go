package main

// sanitizeToolChoice normalizes tool_choice from string to object.
// Some clients send tool_choice: "auto" instead of {"type":"auto"}.
func sanitizeToolChoice(body map[string]any) {
	tc, ok := body["tool_choice"]
	if !ok {
		return
	}
	if s, ok := tc.(string); ok {
		body["tool_choice"] = map[string]any{"type": s}
	}
}

// stripEmptyImageBlocks removes image blocks with empty base64 data from message content.
func stripEmptyImageBlocks(msgs any) {
	arr, _ := msgs.([]any)
	if arr == nil {
		return
	}
	for _, m := range arr {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		content, _ := mm["content"].([]any)
		if content == nil {
			continue
		}
		filtered := make([]any, 0, len(content))
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "image" {
				if src, ok := block["source"].(map[string]any); ok {
					data, _ := src["data"].(string)
					b64, _ := src["base64"].(string)
					if data == "" && b64 == "" {
						continue
					}
				}
			}
			filtered = append(filtered, c)
		}
		mm["content"] = filtered
	}
}
