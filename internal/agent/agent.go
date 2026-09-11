// Package agent runs the model ↔ tools loop. Plain chat is the same loop with
// no tools: it simply ends after the first assistant reply.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"minillm/internal/llm"
)

// Tool is something the model can call in agent mode.
type Tool interface {
	Name() string
	Schema() llm.ToolDef
	NeedsApproval(args json.RawMessage) bool
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is an ordered set of tools. A nil Registry is empty.
type Registry struct {
	tools []Tool
}

func NewRegistry(tools ...Tool) *Registry { return &Registry{tools: tools} }

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

func (r *Registry) Get(name string) Tool {
	if r == nil {
		return nil
	}
	for _, t := range r.tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

func (r *Registry) Defs() []llm.ToolDef {
	if r == nil {
		return nil
	}
	defs := make([]llm.ToolDef, len(r.tools))
	for i, t := range r.tools {
		defs[i] = t.Schema()
	}
	return defs
}

// Mode is what distinguishes chat from agent: a system prompt and a tool set.
type Mode struct {
	Name         string
	SystemPrompt string
	Tools        *Registry
}

// Streamer is the part of llm.Client the loop needs.
type Streamer interface {
	Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Result, error)
}

type Approval int

const (
	Deny Approval = iota
	Allow
	AllowAlways
)

// Event is emitted by Loop.Run. The channel always ends with Done and is then
// closed; consumers must drain it.
type Event interface{ event() }

type (
	TextDelta      struct{ Text string }
	ReasoningDelta struct{ Text string }
	// Appended carries a message that became part of the history.
	Appended      struct{ Msg llm.Message }
	ToolCallStart struct{ Call llm.ToolCall }
	ToolResult    struct {
		Call   llm.ToolCall
		Output string
		Failed bool
	}
	// ApprovalRequest pauses the loop until the consumer answers on Reply.
	ApprovalRequest struct {
		Call  llm.ToolCall
		Reply chan<- Approval
	}
	UsageInfo struct{ Usage llm.Usage }
	Done      struct{ Err error }
)

func (TextDelta) event()       {}
func (ReasoningDelta) event()  {}
func (Appended) event()        {}
func (ToolCallStart) event()   {}
func (ToolResult) event()      {}
func (ApprovalRequest) event() {}
func (UsageInfo) event()       {}
func (Done) event()            {}

type Loop struct {
	LLM         Streamer
	Model       string
	Temperature *float64
	MaxTokens   int
	MaxSteps    int // 0 means 50

	mu      sync.Mutex
	allowed map[string]bool // tools the user allowed for the rest of the session
}

// Run continues the conversation in history (without the system prompt, which
// comes from mode) until the model stops calling tools.
func (l *Loop) Run(ctx context.Context, history []llm.Message, mode Mode) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		ch <- Done{Err: l.run(ctx, history, mode, ch)}
	}()
	return ch
}

func (l *Loop) run(ctx context.Context, history []llm.Message, mode Mode, ch chan<- Event) error {
	msgs := make([]llm.Message, 0, len(history)+1)
	if mode.SystemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: mode.SystemPrompt})
	}
	msgs = append(msgs, history...)

	maxSteps := l.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 50
	}
	for range maxSteps {
		res, err := l.LLM.Stream(ctx, llm.Request{
			Model:       l.Model,
			Messages:    msgs,
			Tools:       mode.Tools.Defs(),
			Temperature: l.Temperature,
			MaxTokens:   l.MaxTokens,
		}, func(d llm.Delta) {
			if d.Reasoning != "" {
				ch <- ReasoningDelta{d.Reasoning}
			}
			if d.Content != "" {
				ch <- TextDelta{d.Content}
			}
		})
		if res.Usage != nil {
			ch <- UsageInfo{*res.Usage}
		}
		msg := res.Message
		if err != nil {
			// Keep the partial answer; drop half-streamed tool calls.
			if msg.Content != "" || msg.Reasoning != "" {
				msg.ToolCalls = nil
				ch <- Appended{msg}
			}
			return err
		}
		msgs = append(msgs, msg)
		ch <- Appended{msg}
		if len(msg.ToolCalls) == 0 {
			return nil
		}
		// Every call gets a result, even after cancellation, so the history
		// stays valid for the next request.
		for _, call := range msg.ToolCalls {
			ch <- ToolCallStart{call}
			out, failed := l.exec(ctx, mode.Tools, call, ch)
			ch <- ToolResult{Call: call, Output: out, Failed: failed}
			tm := llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: out}
			msgs = append(msgs, tm)
			ch <- Appended{tm}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("the agent hit its limit of %d steps", maxSteps)
}

const cancelled = "Cancelled by the user."

func (l *Loop) exec(ctx context.Context, reg *Registry, call llm.ToolCall, ch chan<- Event) (string, bool) {
	if ctx.Err() != nil {
		return cancelled, true
	}
	name := call.Function.Name
	tool := reg.Get(name)
	if tool == nil {
		return fmt.Sprintf("error: unknown tool %q", name), true
	}
	args := json.RawMessage(call.Function.Arguments)
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return "error: arguments are not valid JSON", true
	}
	if tool.NeedsApproval(args) && !l.isAllowed(name) {
		reply := make(chan Approval, 1)
		ch <- ApprovalRequest{Call: call, Reply: reply}
		select {
		case a := <-reply:
			switch a {
			case Deny:
				return "The user denied this tool call.", true
			case AllowAlways:
				l.allow(name)
			}
		case <-ctx.Done():
			return cancelled, true
		}
	}
	out, err := tool.Run(ctx, args)
	if err != nil {
		if out != "" {
			out += "\n"
		}
		return out + "error: " + err.Error(), true
	}
	return out, false
}

func (l *Loop) isAllowed(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allowed[name]
}

func (l *Loop) allow(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.allowed == nil {
		l.allowed = map[string]bool{}
	}
	l.allowed[name] = true
}
