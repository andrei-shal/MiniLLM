package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sse(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&b, "data: %s\n\n", c)
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestReadStreamContentReasoningUsage(t *testing.T) {
	body := sse(
		`{"choices":[{"delta":{"role":"assistant","content":null,"reasoning_content":"hmm "}}]}`,
		`{"choices":[{"delta":{"reasoning":"ok"}}]}`,
		`{"choices":[{"delta":{"content":"\n\nHel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`,
	)
	var got []Delta
	res, err := readStream(strings.NewReader(body), func(d Delta) { got = append(got, d) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Content != "Hello" || res.Message.Reasoning != "hmm ok" {
		t.Fatalf("got content %q reasoning %q", res.Message.Content, res.Message.Reasoning)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 13 || res.FinishReason != "stop" {
		t.Fatalf("usage/finish: %+v %q", res.Usage, res.FinishReason)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 deltas, got %d: %+v", len(got), got)
	}
}

func TestReadStreamToolCalls(t *testing.T) {
	body := sse(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"read","arguments":"{}"}}]}}]}`,
		// A server that reuses index 0 for a new call.
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"ls","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
	)
	res, err := readStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := res.Message.ToolCalls
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %+v", calls)
	}
	if calls[0].Function.Name != "bash" || calls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Fatalf("bad first call: %+v", calls[0])
	}
	if calls[1].ID != "b" || calls[2].ID != "c" || calls[2].Function.Name != "ls" {
		t.Fatalf("bad calls: %+v", calls)
	}
}

func TestReadStreamErrorChunk(t *testing.T) {
	body := sse(
		`{"choices":[{"delta":{"content":"par"}}]}`,
		`{"error":{"message":"overloaded"}}`,
	)
	res, err := readStream(strings.NewReader(body), nil)
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("expected overloaded error, got %v", err)
	}
	if res.Message.Content != "par" {
		t.Fatalf("partial content lost: %q", res.Message.Content)
	}
}

func TestThinkSplitter(t *testing.T) {
	// Feed byte by byte to exercise tags split across chunks.
	in := "  <think>let me think</think>\n\nanswer <think> stays"
	var ts thinkSplitter
	var content, reasoning string
	for _, r := range in {
		c, rs := ts.push(string(r))
		content += c
		reasoning += rs
	}
	c, rs := ts.flush()
	content += c
	reasoning += rs
	if reasoning != "let me think" || content != "answer <think> stays" {
		t.Fatalf("content %q reasoning %q", content, reasoning)
	}

	var plain thinkSplitter
	c1, _ := plain.push("<th")
	c2, _ := plain.push("e best")
	if c1+c2 != "<the best" {
		t.Fatalf("plain content mangled: %q", c1+c2)
	}
}

func TestStreamRetriesWithoutStreamOptions(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(raw, &req)
		if _, ok := req["stream_options"]; ok {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"unknown field stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sse(`{"choices":[{"delta":{"content":"hi"}}]}`)))
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "k", nil)
	for range 2 {
		res, err := c.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "x"}}}, nil)
		if err != nil || res.Message.Content != "hi" {
			t.Fatalf("res %+v err %v", res, err)
		}
	}
	if calls != 3 { // 400 + retry, then straight without stream_options
		t.Fatalf("expected 3 requests, got %d", calls)
	}
}

func TestNonStreamingFallbackAndModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			w.Write([]byte(`{"data":[{"id":"b"},{"id":"a"}]}`))
		default:
			w.Write([]byte(`{"choices":[{"message":{"content":"<think>x</think>yo"},"finish_reason":"stop"}]}`))
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "", nil)
	res, err := c.Stream(context.Background(), Request{Model: "m"}, nil)
	if err != nil || res.Message.Content != "yo" || res.Message.Reasoning != "x" {
		t.Fatalf("res %+v err %v", res.Message, err)
	}
	models, err := c.ListModels(context.Background())
	if err != nil || strings.Join(models, ",") != "a,b" {
		t.Fatalf("models %v err %v", models, err)
	}
}

func TestErrorMessageShapes(t *testing.T) {
	for raw, want := range map[string]string{
		`{"error":{"message":"bad key"}}`: "bad key",
		`{"error":"nope"}`:                "nope",
		`{"detail":"not found"}`:          "not found",
		`plain text`:                      "plain text",
	} {
		if got := errorMessage([]byte(raw)); got != want {
			t.Errorf("%s: got %q want %q", raw, got, want)
		}
	}
}
