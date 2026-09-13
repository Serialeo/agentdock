package app

func evolutionToolSpecs() []ToolSpec {
	return []ToolSpec{{
		Name: "evolve", Contract: evolutionToolContract, Title: "Evolve AgentDock knowledge",
		Description: "Propose reusable knowledge, pre-bind Task learning checks, supersede, or retract. Bindings created after task execution starts are rejected; bind declares on_success/on_failure semantics, AgentDock resolves later Task outcomes, and Recall only persists the result.",
		Annotations: mutatingToolAnnotations(true, false), Availability: requiresNexus, Handler: ctxToolHandler((*Runtime).evolve),
	}}
}
