package agent

// ChatMode talks to the model as is.
func ChatMode(system string) Mode {
	return Mode{Name: "chat", SystemPrompt: system}
}

// AgentMode is the same conversation with tools enabled.
func AgentMode(system string) Mode {
	return Mode{Name: "agent", SystemPrompt: system, Tools: NewRegistry(agentTools()...)}
}

// agentTools lists the tools available in agent mode. Empty for now: tools
// (bash, read, write, edit, …) plug in here without touching the loop or UI.
func agentTools() []Tool { return nil }
