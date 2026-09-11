package ui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"minillm/internal/agent"
	"minillm/internal/llm"
)

// OneShot answers a single prompt on stdout. With raw, text is streamed as is
// (for pipes); otherwise finished markdown blocks are rendered as they arrive.
func OneShot(o Options, prompt string, raw bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sess := o.Session
	sess.Messages = append(sess.Messages, llm.Message{Role: llm.RoleUser, Content: prompt})
	loop := &agent.Loop{
		LLM:         llm.New(o.Provider.BaseURL, o.Provider.Key(), o.Provider.Headers),
		Model:       o.Model,
		Temperature: o.Config.Chat.Temperature,
		MaxTokens:   o.Config.Chat.MaxTokens,
	}
	mode := agent.ChatMode(sess.System)
	if o.Agent {
		mode = agent.AgentMode(sess.System)
	}
	sess.Mode = mode.Name

	th, md := newTheme(o.Dark), &markdown{dark: o.Dark}
	errTTY := term.IsTerminal(int(os.Stderr.Fd()))
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width = w
	}

	var (
		split     splitter
		printed   bool
		thinkFrom time.Time
		runErr    error
	)
	block := func(b string) {
		if printed {
			fmt.Println()
		}
		lipgloss.Println(md.render(b, width))
		printed = true
	}
	endThink := func() {
		if thinkFrom.IsZero() {
			return
		}
		if errTTY {
			fmt.Fprint(os.Stderr, "\r\x1b[2K")
			lipgloss.Fprintln(os.Stderr, th.Muted.Render("✻ thought for "+fmtDur(time.Since(thinkFrom))))
		}
		thinkFrom = time.Time{}
	}

	for ev := range loop.Run(ctx, sess.Messages, mode) {
		switch e := ev.(type) {
		case agent.ReasoningDelta:
			if thinkFrom.IsZero() {
				thinkFrom = time.Now()
				if errTTY {
					fmt.Fprint(os.Stderr, lipgloss.Sprint(th.Muted.Render("✻ thinking…")))
				}
			}
		case agent.TextDelta:
			endThink()
			if raw {
				fmt.Print(e.Text)
				continue
			}
			for _, b := range split.push(e.Text) {
				block(b)
			}
		case agent.Appended:
			if e.Msg.Role == llm.RoleAssistant {
				endThink()
				if raw && e.Msg.Content != "" {
					fmt.Println()
				} else if rest := split.flush(); rest != "" {
					block(rest)
				}
			}
			sess.Messages = append(sess.Messages, e.Msg)
		case agent.ToolCallStart:
			fmt.Fprintf(os.Stderr, "⏺ %s(%s)\n", e.Call.Function.Name, summarizeArgs(e.Call.Function.Arguments, 60))
		case agent.ApprovalRequest:
			fmt.Fprintln(os.Stderr, "  ⎿ denied: approvals only work in interactive mode")
			e.Reply <- agent.Deny
		case agent.Done:
			runErr = e.Err
		}
	}
	endThink()
	// Don't leave an unanswered question for `mllm -c` to continue from.
	if n := len(sess.Messages); n > 0 && sess.Messages[n-1].Role == llm.RoleUser {
		sess.Messages = sess.Messages[:n-1]
	}
	if err := sess.Save(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}
