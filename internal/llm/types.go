// Package llm is a minimal streaming client for OpenAI-compatible chat
// completion APIs (/v1/chat/completions, /v1/models).
package llm

import "encoding/json"

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one conversation entry. Reasoning is kept locally for display and
// sessions but is never sent back to the provider.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Reasoning  string     `json:"reasoning,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	Temperature *float64
	MaxTokens   int
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Delta is an incremental piece of a streamed response.
type Delta struct {
	Content   string
	Reasoning string
}

// Result is the assembled response. On error it holds whatever arrived before
// the failure, so a cancelled answer can still be kept.
type Result struct {
	Message      Message
	Usage        *Usage
	FinishReason string
}
