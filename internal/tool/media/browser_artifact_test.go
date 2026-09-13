package media

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func TestPublishBrowserScreenshotProducesReadableArtifact(t *testing.T) {
	service, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "browser-source.png")
	writeTinyPNG(t, imagePath)
	png, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.PublishBrowserScreenshot(context.Background(), png, 60)
	if err != nil {
		t.Fatal(err)
	}
	artifactID, _ := result["artifact_id"].(string)
	if artifactID == "" || result["mime_type"] != "image/png" || result["filename"] != "browser-screenshot.png" {
		t.Fatalf("browser screenshot Artifact result = %#v", result)
	}
	store := publicartifacts.New(service.cfg.AgentDockHome, service.cfg.OAuthServerURL, service.cfg.Port)
	metadata, payload, eof, err := store.ReadChunk(artifactID, 0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.MimeType != "image/png" || !eof || !bytes.Equal(payload, png) {
		t.Fatalf("browser screenshot Artifact read = metadata:%#v bytes:%d eof:%t", metadata, len(payload), eof)
	}
}

func TestPublishBrowserScreenshotRejectsNonImageBytes(t *testing.T) {
	service, _ := newMediaTestService(t)
	if _, err := service.PublishBrowserScreenshot(context.Background(), []byte("not a png"), 60); err == nil {
		t.Fatal("invalid browser screenshot bytes were accepted")
	}
}
