package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"minillm/internal/llm"
)

// scripted returns canned results in order and records the requests.
type scripted struct {
	results []llm.Result
	reqs    []llm.Request
}

func (s *scripted) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Result, error) {
	s.reqs = append(s.reqs, req)
	res := s.results[0]
	s.results = s.results[1:]
	if res.Message.Content != "" {
		onDelta(llm.Delta{Content: res.Message.Content})
	}
	return res, nil
}

type echoTool struct {
	approve bool
	runs    int
}

func (e *echoTool) Name() string { return "echo" }
func (e *echoTool) Schema() llm.ToolDef {
	return llm.ToolDef{Type: "function", Function: llm.FunctionDef{Name: "echo"}}
}
func (e *echoTool) NeedsApproval(json.RawMessage) bool { return e.approve }
func (e *echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	e.runs++
	return "echo:" + string(args), nil
}

func assistant(content string, calls ...llm.ToolCall) llm.Result {
	return llm.Result{Message: llm.Message{Role: llm.RoleAssistant, Content: content, ToolCalls: calls}}
}

func call(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: "echo", Arguments: `{"x":1}`}}
}

// collect drains the loop, answering approval requests with answer.
func collect(t *testing.T, ch <-chan Event, answer Approval) (appended []llm.Message, text string, err error) {
	t.Helper()
	for ev := range ch {
		switch e := ev.(type) {
		case TextDelta:
			text += e.Text
		case Appended:
			appended = append(appended, e.Msg)
		case ApprovalRequest:
			e.Reply <- answer
		case Done:
			err = e.Err
		}
	}
	return
}

func TestChatModeSingleTurn(t *testing.T) {
	s := &scripted{results: []llm.Result{assistant("hi")}}
	l := &Loop{LLM: s, Model: "m"}
	msgs, text, err := collect(t, l.Run(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "yo"}}, ChatMode("sys")), Allow)
	if err != nil || text != "hi" || len(msgs) != 1 {
		t.Fatalf("msgs %+v text %q err %v", msgs, text, err)
	}
	if r := s.reqs[0]; r.Messages[0].Role != llm.RoleSystem || len(r.Tools) != 0 {
		t.Fatalf("bad request: %+v", r)
	}
}

func TestToolLoop(t *testing.T) {
	tool := &echoTool{}
	s := &scripted{results: []llm.Result{assistant("", call("1"), call("2")), assistant("done")}}
	l := &Loop{LLM: s}
	mode := Mode{Tools: NewRegistry(tool)}
	msgs, _, err := collect(t, l.Run(context.Background(), nil, mode), Allow)
	if err != nil {
		t.Fatal(err)
	}
	// assistant(calls), tool, tool, assistant
	if len(msgs) != 4 || msgs[1].ToolCallID != "1" || msgs[2].Content != `echo:{"x":1}` || msgs[3].Content != "done" {
		t.Fatalf("bad history: %+v", msgs)
	}
	if tool.runs != 2 || len(s.reqs[1].Messages) != 3 || len(s.reqs[0].Tools) != 1 {
		t.Fatalf("runs %d, second request %+v", tool.runs, s.reqs[1].Messages)
	}
}

func TestApprovalDenyAndAlways(t *testing.T) {
	tool := &echoTool{approve: true}
	s := &scripted{results: []llm.Result{assistant("", call("1")), assistant("ok")}}
	l := &Loop{LLM: s}
	msgs, _, _ := collect(t, l.Run(context.Background(), nil, Mode{Tools: NewRegistry(tool)}), Deny)
	if tool.runs != 0 || msgs[1].Content != "The user denied this tool call." {
		t.Fatalf("denied tool ran or wrong result: %+v", msgs)
	}

	s.results = []llm.Result{assistant("", call("2")), assistant("", call("3")), assistant("ok")}
	asked := 0
	for ev := range l.Run(context.Background(), nil, Mode{Tools: NewRegistry(tool)}) {
		if a, ok := ev.(ApprovalRequest); ok {
			asked++
			a.Reply <- AllowAlways
		}
	}
	if asked != 1 || tool.runs != 2 {
		t.Fatalf("asked %d times, ran %d times", asked, tool.runs)
	}
}

func TestCancelledKeepsHistoryValid(t *testing.T) {
	tool := &echoTool{approve: true}
	s := &scripted{results: []llm.Result{assistant("", call("1"), call("2"))}}
	l := &Loop{LLM: s}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var msgs []llm.Message
	var err error
	for ev := range l.Run(ctx, nil, Mode{Tools: NewRegistry(tool)}) {
		switch e := ev.(type) {
		case ApprovalRequest:
			cancel() // user hits Esc instead of answering
		case Appended:
			msgs = append(msgs, e.Msg)
		case Done:
			err = e.Err
		}
	}
	if !errors.Is(err, context.Canceled) || tool.runs != 0 {
		t.Fatalf("err %v runs %d", err, tool.runs)
	}
	if len(msgs) != 3 || msgs[1].Content != cancelled || msgs[2].Content != cancelled {
		t.Fatalf("every call needs a result: %+v", msgs)
	}
}
