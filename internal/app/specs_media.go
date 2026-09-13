package app

import (
	"context"

	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
)

func imageToolSpecs() []ToolSpec {
	return []ToolSpec{{Name: "view_image", Contract: mediaToolContract, Title: "View image", Description: "Load an image by AgentDock artifact_id, Host path, or HTTP(S) URL and return it as standard MCP image content.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("view_image", func(ctx context.Context, r *Runtime, request toolmedia.ViewImageRequest) (Result, error) {
		return r.media.ViewImage(ctx, request)
	})}}
}
