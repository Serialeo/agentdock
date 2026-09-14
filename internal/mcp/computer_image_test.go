package mcp

import (
	"github.com/uvwt/agentdock/internal/app"
	"testing"
)

func TestComputerImagesRemainPrivateMCPContent(t *testing.T) {
	for _, name := range []string{"computer_observe", "computer_act"} {
		envelope := toolEnvelope(name, app.Result{"observation_id": "obs", "_mcp_image_base64": "aW1hZ2U=", "_mcp_image_mime_type": "image/png"}, nil)
		content := envelope["content"].([]map[string]any)
		if content[0]["type"] != "image" || content[0]["mimeType"] != "image/png" {
			t.Fatal(envelope)
		}
		structured := envelope["structuredContent"].(map[string]any)
		if structured["observation_id"] != "obs" || structured["_mcp_image_base64"] != nil || structured["url"] != nil {
			t.Fatal(structured)
		}
	}
	envelope := toolEnvelope("computer_status", app.Result{"_mcp_image_base64": "aW1hZ2U="}, nil)
	if envelope["content"].([]map[string]any)[0]["type"] != "text" {
		t.Fatal("untrusted image conversion")
	}
}
