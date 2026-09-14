package app

import "context"

func browserToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "browser_session", Contract: browserToolContract, Title: "Browser session", Description: "Start an AgentDock-owned Chromium-family browser or attach to an existing CDP browser with a dedicated AgentDock target, then close or clean up the session. External browsers remain running when the session closes.", Annotations: mutatingToolAnnotations(true, true), Group: "browser", Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
			return r.handleProjectBrowserSession(ctx, args)
		}},
		{Name: "browser_act", Contract: browserToolContract, Title: "Browser actions", Description: "Run strictly validated CSS/CDP browser actions against an AgentDock-managed browser target and return the final typed page snapshot plus screenshot Artifact.", Annotations: mutatingToolAnnotations(true, true), Group: "browser", Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
			return r.handleProjectBrowserAct(ctx, args)
		}},
		{Name: "browser_snapshot", Contract: browserToolContract, Title: "Browser snapshot", Description: "Capture the active or requested CDP target with page text, viewport, page size, focus, visible interactive elements, diagnostics, and a PNG screenshot Artifact.", Annotations: readOnlyToolAnnotations(true), Group: "browser", Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
			return r.handleProjectBrowserSnapshot(ctx, args)
		}},
	}
}
