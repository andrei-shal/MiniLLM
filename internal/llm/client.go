package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

type Client struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
	HTTP    *http.Client

	// Set once the provider rejects stream_options, so we stop sending it.
	noStreamOptions atomic.Bool
}

func New(baseURL, apiKey string, headers map[string]string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Headers: headers,
		HTTP:    &http.Client{},
	}
}

// APIError is a non-2xx response or an error object inside the stream.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	Tools         []ToolDef      `json:"tools,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

func toWire(req Request, withUsage bool) wireRequest {
	w := wireRequest{
		Model:       req.Model,
		Tools:       req.Tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}
	if withUsage {
		w.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
		// Assistant turns that only call tools carry a null content.
		if m.Content != "" || len(m.ToolCalls) == 0 {
			c := m.Content
			wm.Content = &c
		}
		w.Messages = append(w.Messages, wm)
	}
	return w
}

// Stream sends a chat completion request and calls onDelta for every piece of
// text or reasoning as it arrives. The returned Result is valid even on error.
func (c *Client) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Result, error) {
	withUsage := !c.noStreamOptions.Load()
	resp, err := c.post(ctx, toWire(req, withUsage))
	var apiErr *APIError
	if withUsage && errors.As(err, &apiErr) && apiErr.Status < 500 &&
		strings.Contains(strings.ToLower(apiErr.Message), "stream_options") {
		c.noStreamOptions.Store(true)
		resp, err = c.post(ctx, toWire(req, false))
	}
	if err != nil {
		return Result{Message: Message{Role: RoleAssistant}}, err
	}
	defer resp.Body.Close()

	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct == "application/json" {
		return readCompletion(resp.Body, onDelta)
	}
	return readStream(resp.Body, onDelta)
}

func (c *Client) post(ctx context.Context, body wireRequest) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return c.do(req)
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	return resp, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "minillm")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
}

// ListModels returns the model IDs from GET /models, sorted.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("не удалось разобрать список моделей: %w", err)
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// errorMessage extracts a human-readable message from an error body in any of
// the shapes providers use.
func errorMessage(raw []byte) string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(raw, &body) == nil {
		if msg := rawErrorMessage(body.Error); msg != "" {
			return msg
		}
		if body.Message != "" {
			return body.Message
		}
		if msg := rawErrorMessage(body.Detail); msg != "" {
			return msg
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	if s == "" {
		s = "пустой ответ"
	}
	return s
}

func rawErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return string(raw)
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage          `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// assembler accumulates deltas into a Result.
type assembler struct {
	res       Result
	content   strings.Builder
	reasoning strings.Builder
	think     thinkSplitter
	onDelta   func(Delta)
}

func (a *assembler) emit(content, reasoning string) {
	c, r := a.think.push(content)
	a.add(c, reasoning+r)
}

func (a *assembler) add(content, reasoning string) {
	if content == "" && reasoning == "" {
		return
	}
	a.content.WriteString(content)
	a.reasoning.WriteString(reasoning)
	if a.onDelta != nil {
		a.onDelta(Delta{Content: content, Reasoning: reasoning})
	}
}

func (a *assembler) result() Result {
	a.add(a.think.flush())
	a.res.Message.Role = RoleAssistant
	a.res.Message.Content = strings.TrimRight(a.content.String(), " \t\r\n")
	a.res.Message.Reasoning = strings.TrimSpace(a.reasoning.String())
	calls := a.res.Message.ToolCalls[:0]
	for i, tc := range a.res.Message.ToolCalls {
		if tc.Function.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d", i)
		}
		tc.Type = "function"
		calls = append(calls, tc)
	}
	a.res.Message.ToolCalls = calls
	return a.res
}

func readStream(r io.Reader, onDelta func(Delta)) (Result, error) {
	a := &assembler{onDelta: onDelta}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:"); ok {
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				break
			}
			if data != "" {
				if perr := a.chunk(data); perr != nil {
					return a.result(), perr
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return a.result(), err
		}
	}
	return a.result(), nil
}

func (a *assembler) chunk(data string) error {
	var ch streamChunk
	if err := json.Unmarshal([]byte(data), &ch); err != nil {
		return fmt.Errorf("битый чанк от сервера: %w", err)
	}
	if msg := rawErrorMessage(ch.Error); msg != "" {
		return &APIError{Message: msg}
	}
	if ch.Usage != nil {
		a.res.Usage = ch.Usage
	}
	for _, choice := range ch.Choices {
		d := choice.Delta
		reasoning := d.ReasoningContent
		if reasoning == "" {
			reasoning = d.Reasoning
		}
		a.emit(d.Content, reasoning)
		calls := &a.res.Message.ToolCalls
		for _, tc := range d.ToolCalls {
			i := tc.Index
			// Some servers reuse index 0 for every call; a new ID means a new call.
			if i < len(*calls) && tc.ID != "" && (*calls)[i].ID != "" && (*calls)[i].ID != tc.ID {
				i = len(*calls)
			}
			for len(*calls) <= i {
				*calls = append(*calls, ToolCall{Type: "function"})
			}
			call := &(*calls)[i]
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if call.Function.Name == "" {
				call.Function.Name = tc.Function.Name
			}
			call.Function.Arguments += tc.Function.Arguments
		}
		if choice.FinishReason != "" {
			a.res.FinishReason = choice.FinishReason
		}
	}
	return nil
}

// readCompletion handles servers that ignore "stream": true and answer with a
// single JSON object.
func readCompletion(r io.Reader, onDelta func(Delta)) (Result, error) {
	a := &assembler{onDelta: onDelta}
	var body struct {
		Choices []struct {
			Message struct {
				Content          string     `json:"content"`
				ReasoningContent string     `json:"reasoning_content"`
				Reasoning        string     `json:"reasoning"`
				ToolCalls        []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage          `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return a.result(), fmt.Errorf("не удалось разобрать ответ: %w", err)
	}
	if msg := rawErrorMessage(body.Error); msg != "" {
		return a.result(), &APIError{Message: msg}
	}
	a.res.Usage = body.Usage
	if len(body.Choices) > 0 {
		m := body.Choices[0].Message
		reasoning := m.ReasoningContent
		if reasoning == "" {
			reasoning = m.Reasoning
		}
		a.emit(m.Content, reasoning)
		a.res.Message.ToolCalls = m.ToolCalls
		a.res.FinishReason = body.Choices[0].FinishReason
	}
	return a.result(), nil
}
