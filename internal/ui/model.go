package ui

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"minillm/internal/agent"
	"minillm/internal/config"
	"minillm/internal/llm"
	"minillm/internal/session"
)

type Options struct {
	Config      *config.Config
	Session     *session.Session
	Provider    *config.Provider
	Model       string
	Agent       bool
	Dark        bool
	DetectTheme bool // ask the terminal for its background color
	Version     string
}

type (
	eventMsg   struct{ ev agent.Event }
	printedMsg struct{}
	modelsMsg  struct {
		provider string
		models   []string
		err      error
	}
	startTimeoutMsg struct{}
)

// How long to wait at startup for the terminal to report its background color
// and the cursor position; terminals that don't support a query never answer.
const startTimeout = 300 * time.Millisecond

type Model struct {
	cfg     *config.Config
	sess    *session.Session
	th      theme
	md      *markdown
	version string
	detect  bool

	width, height int
	sized         bool
	themeReady    bool
	posReady      bool
	started       bool
	quitting      bool

	input   textarea.Model
	spin    spinner.Model
	history *inputHistory
	compSel int // selected slash-command completion

	provider  *config.Provider
	model     string
	loop      *agent.Loop
	agentMode bool

	// The turn being streamed.
	busy      bool
	cancel    context.CancelFunc
	events    <-chan agent.Event
	approval  *agent.ApprovalRequest
	split     splitter
	reasoning strings.Builder
	thinkFrom time.Time
	thinkDone bool
	gotText   bool
	turnFrom  time.Time
	queued    string

	// Cached render of the unfinished block.
	live    string
	liveSrc string
	liveAt  time.Time

	usage     llm.Usage
	showThink bool

	// The frame owns the latest conversation lines (the tail) and hands the
	// oldest to the scrollback only when they no longer fit. Menus and pickers
	// borrow rows from the tail and give them back, instead of scrolling the
	// terminal and leaving a gap under the input when they close.
	tail     []string
	top      int      // the frame's first row on screen
	lastH    int      // height of the last frame
	outbox   []string // tail lines on their way to the scrollback
	printing bool     // one print at a time keeps them in order
	meter    frameMeter

	picker      *picker
	modelsCache map[string][]string
	notice      string
	exitArmed   bool
}

// Run starts the interactive chat.
func Run(o Options) error {
	m := newModel(o)
	_, err := tea.NewProgram(m).Run()
	m.sess.Save()
	return err
}

func newModel(o Options) *Model {
	m := &Model{
		cfg:         o.Config,
		sess:        o.Session,
		version:     o.Version,
		detect:      o.DetectTheme,
		themeReady:  !o.DetectTheme,
		width:       80,
		height:      24,
		agentMode:   o.Agent,
		history:     loadHistory(filepath.Join(config.DataDir(), "history")),
		modelsCache: map[string][]string{},
		input:       newInput(),
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	m.applyTheme(o.Dark)
	m.setModel(o.Provider, o.Model)
	m.sess.Mode = m.mode().Name
	return m
}

func newInput() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Placeholder = "Ask anything…  (/ for commands)"
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	ta.MaxContentHeight = 10000
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter", "ctrl+j"))
	ta.SetVirtualCursor(true) // the terminal cursor is parked, see parkedCursor
	ta.Focus()
	return ta
}

func (m *Model) applyTheme(dark bool) {
	th := newTheme(dark)
	m.th, m.md = th, &markdown{dark: dark}
	m.spin.Style = th.Accent

	prompt := th.Accent.Bold(true).Render("❯ ")
	m.input.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return prompt
		}
		return "  "
	})
	st := textarea.DefaultStyles(dark)
	plain := lipgloss.NewStyle()
	st.Focused.Base, st.Focused.Text, st.Focused.CursorLine, st.Focused.Prompt = plain, plain, plain, plain
	st.Focused.Placeholder = th.Faint
	st.Cursor.Color = th.accent
	st.Cursor.Blink = false
	m.input.SetStyles(st)
	m.input.SetWidth(max(10, m.width-4))
}

func (m *Model) ref() string { return m.provider.Name + "/" + m.model }

func (m *Model) setModel(p *config.Provider, model string) {
	if m.loop == nil {
		m.loop = &agent.Loop{Temperature: m.cfg.Chat.Temperature, MaxTokens: m.cfg.Chat.MaxTokens}
	}
	if m.provider != p {
		m.loop.LLM = llm.New(p.BaseURL, p.Key(), p.Headers)
	}
	m.provider, m.model = p, model
	m.loop.Model = model
	m.sess.Model = m.ref()
}

func (m *Model) mode() agent.Mode {
	if m.agentMode {
		return agent.AgentMode(m.sess.System)
	}
	return agent.ChatMode(m.sess.System)
}

func (m *Model) Init() tea.Cmd {
	// The cursor is where the frame will start.
	cmds := []tea.Cmd{tea.RequestCursorPosition, tea.Tick(startTimeout, func(time.Time) tea.Msg { return startTimeoutMsg{} })}
	if m.detect {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// maybeStart prints the banner once the size, colors and position are known.
func (m *Model) maybeStart() {
	if m.started || !m.sized || !m.themeReady || !m.posReady {
		return
	}
	m.started = true
	m.print(m.banner())
	if len(m.sess.Messages) > 0 {
		m.print(m.transcript(m.sess.Messages))
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		widthChanged := msg.Width != m.width
		m.width, m.height, m.sized = msg.Width, msg.Height, true
		m.input.SetWidth(max(10, m.width-4))
		m.live, m.liveSrc = "", ""
		if m.started {
			if widthChanged && len(m.tail) > 0 {
				m.tail = strings.Split(fitWidth(strings.Join(m.tail, "\n"), m.width), "\n")
			}
			// Terminals move content around on resize; ask where the frame is.
			cmds = append(cmds, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tea.RequestCursorPosition() }))
		}
		m.maybeStart()
	case tea.CursorPositionMsg:
		m.top, m.posReady = msg.Y, true // the cursor is parked on the frame's first row
		m.maybeStart()
	case tea.BackgroundColorMsg:
		if !m.themeReady {
			m.applyTheme(msg.IsDark())
			m.themeReady = true
			m.maybeStart()
		}
	case startTimeoutMsg:
		m.themeReady = true
		if !m.posReady {
			m.top, m.posReady = max(0, m.height-1), true // most likely under earlier output
		}
		m.maybeStart()
	case printedMsg:
		m.printing = false
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
	case eventMsg:
		cmds = append(cmds, m.onEvent(msg.ev))
	case modelsMsg:
		m.onModels(msg)
	case tea.KeyPressMsg:
		cmds = append(cmds, m.onKey(msg))
	case tea.PasteMsg:
		if m.picker != nil {
			m.picker.type_(oneLine(msg.Content))
			break
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.flush())
	return m, tea.Batch(cmds...)
}

// print adds text to the conversation. A leading "\n" gives a blank line.
func (m *Model) print(s string) {
	m.tail = append(m.tail, strings.Split(fitWidth(s, m.width), "\n")...)
}

// flush hands the tail lines that no longer fit in the frame to the
// scrollback.
func (m *Model) flush() tea.Cmd {
	// With nothing open, the frame (a row short of the screen at most) holds
	// the tail, a spacer, a one-line input and the status line.
	n := max(0, len(m.tail)-max(1, m.height-6))
	// tmux (scroll-on-clear) copies the whole screen into its history when a
	// redraw erases from the top-left corner, so the frame must not start on
	// the first row: a conversation line always sits above it.
	if n == 0 && m.top == 0 && len(m.tail) > 0 && len(m.outbox) == 0 {
		n = 1
	}
	if n > 0 {
		m.outbox = append(m.outbox, m.tail[:n]...)
		m.tail = slices.Clone(m.tail[n:])
	}
	if m.printing || len(m.outbox) == 0 {
		return nil
	}
	m.printing = true
	return tea.Sequence(m.takeOutbox(), func() tea.Msg { return printedMsg{} })
}

func (m *Model) takeOutbox() tea.Cmd {
	lines := m.outbox
	m.outbox = nil
	// Printing pushes a frame that isn't at the bottom of the screen down.
	m.top = min(m.top+len(lines), max(0, m.height-m.lastH))
	return printChunks(strings.Join(lines, "\n"), m.height-m.meter.tall)
}

func (m *Model) quit() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.quitting = true
	m.save()
	if len(m.outbox) > 0 {
		return tea.Sequence(m.takeOutbox(), tea.Quit)
	}
	return tea.Quit
}

func (m *Model) save() {
	if err := m.sess.Save(); err != nil {
		m.print(m.th.Error.Render("✗ couldn't save the conversation: " + err.Error()))
	}
}

func (m *Model) onKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	m.notice = ""
	if key != "ctrl+c" {
		m.exitArmed = false
	}
	if m.approval != nil {
		return m.onApprovalKey(key)
	}
	if m.picker != nil {
		cmd, done := m.picker.key(k)
		if done {
			m.picker = nil
		}
		return cmd
	}

	switch key {
	case "ctrl+c":
		switch {
		case m.busy:
			m.stop()
		case m.input.Value() != "":
			m.input.Reset()
			m.history.reset()
		case m.exitArmed:
			return m.quit()
		default:
			m.exitArmed = true
			m.notice = "press Ctrl+C again to quit"
		}
		return nil
	case "ctrl+d":
		if m.input.Value() == "" {
			return m.quit()
		}
	case "esc":
		if m.busy {
			m.stop()
		}
		return nil
	case "shift+tab":
		m.toggleMode()
		return nil
	case "ctrl+l":
		m.top = 0 // the frame is redrawn at the top of the cleared screen
		return tea.ClearScreen
	case "tab":
		if c := m.completions(); len(c) > 0 {
			m.input.SetValue(c[m.compSel%len(c)].name + " ")
			m.compSel = 0
		}
		return nil
	case "up", "down":
		if c := m.completions(); len(c) > 0 {
			if key == "up" {
				m.compSel = (m.compSel - 1 + len(c)) % len(c)
			} else {
				m.compSel = (m.compSel + 1) % len(c)
			}
			return nil
		}
		if key == "up" && m.input.Line() == 0 {
			if v, ok := m.history.prev(m.input.Value()); ok {
				m.input.SetValue(v)
				return nil
			}
		}
		if key == "down" && m.input.Line() == m.input.LineCount()-1 {
			if v, ok := m.history.next(); ok {
				m.input.SetValue(v)
				return nil
			}
		}
	case "enter":
		// A trailing backslash continues the line, for terminals that can't
		// send Alt+Enter or Shift+Enter.
		if v := m.input.Value(); strings.HasSuffix(v, `\`) {
			m.input.SetValue(strings.TrimSuffix(v, `\`) + "\n")
			return nil
		}
		return m.submit()
	}

	prev := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.input.Value() != prev {
		m.compSel = 0
	}
	return cmd
}

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		if c := m.completions(); len(c) > 0 && lookup(text) == nil {
			text = c[m.compSel%len(c)].name
		}
		name, arg, _ := strings.Cut(text, " ")
		if c := lookup(name); c != nil {
			if c.idle && m.busy {
				m.notice = "wait for the answer to finish (esc to stop)"
				return nil
			}
			m.history.add(text)
			m.input.Reset()
			m.compSel = 0
			return c.run(m, strings.TrimSpace(arg))
		}
		if !strings.Contains(name[1:], "/") {
			m.notice = "unknown command " + name + " — see /help"
			return nil
		}
	}
	m.history.add(text)
	m.input.Reset()
	if m.busy {
		m.queued = text
		return nil
	}
	return m.send(text)
}

func (m *Model) send(text string) tea.Cmd {
	m.sess.Messages = append(m.sess.Messages, llm.Message{Role: llm.RoleUser, Content: text})
	m.print("\n" + m.userBlock(text))
	return m.startTurn()
}

func (m *Model) startTurn() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.busy, m.turnFrom = cancel, true, time.Now()
	m.resetStep()
	m.events = m.loop.Run(ctx, slices.Clone(m.sess.Messages), m.mode())
	return tea.Batch(m.next(), m.spin.Tick)
}

func (m *Model) next() tea.Cmd {
	ch := m.events
	return func() tea.Msg {
		if ev, ok := <-ch; ok {
			return eventMsg{ev}
		}
		return nil
	}
}

// resetStep clears per-response state; the agent loop may produce several
// responses in one turn.
func (m *Model) resetStep() {
	m.split = splitter{}
	m.reasoning.Reset()
	m.thinkFrom, m.thinkDone, m.gotText = time.Time{}, false, false
	m.live, m.liveSrc = "", ""
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.approval = nil
}

func (m *Model) onEvent(ev agent.Event) tea.Cmd {
	switch e := ev.(type) {
	case agent.ReasoningDelta:
		if m.thinkFrom.IsZero() {
			m.thinkFrom = time.Now()
		}
		m.reasoning.WriteString(e.Text)
	case agent.TextDelta:
		m.endThinking()
		m.gotText = true
		for _, b := range m.split.push(e.Text) {
			m.print("\n" + m.md.render(b, m.width))
		}
	case agent.Appended:
		if e.Msg.Role == llm.RoleAssistant {
			m.flushAnswer()
			m.resetStep()
		}
		m.sess.Messages = append(m.sess.Messages, e.Msg)
	case agent.ToolCallStart:
		m.print("\n" + m.toolCallLine(e.Call))
	case agent.ToolResult:
		m.print(m.toolResultLine(e))
	case agent.ApprovalRequest:
		m.approval = &e
	case agent.UsageInfo:
		m.usage = e.Usage
	case agent.Done:
		return m.finishTurn(e.Err)
	}
	return m.next()
}

func (m *Model) flushAnswer() {
	m.endThinking()
	if rest := m.split.flush(); rest != "" {
		m.print("\n" + m.md.render(rest, m.width))
	}
}

func (m *Model) endThinking() {
	if m.thinkDone || m.reasoning.Len() == 0 {
		return
	}
	m.thinkDone = true
	m.print("\n" + m.thinkingBlock(m.reasoning.String(), time.Since(m.thinkFrom)))
}

func (m *Model) finishTurn(err error) tea.Cmd {
	m.flushAnswer()
	m.busy, m.cancel, m.approval, m.events = false, nil, nil, nil
	m.resetStep()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.print(m.th.Muted.Render("  ⎿ stopped"))
		} else {
			m.print("\n" + m.th.Error.Render("✗ "+err.Error()))
		}
		// Nothing came back: take the question out of the history so the next
		// one doesn't follow an unanswered user message, and give it back.
		if n := len(m.sess.Messages); n > 0 && m.sess.Messages[n-1].Role == llm.RoleUser {
			text := m.sess.Messages[n-1].Content
			m.sess.Messages = m.sess.Messages[:n-1]
			if m.input.Value() == "" && m.queued == "" {
				m.input.SetValue(text)
			}
		}
	}
	m.save()
	if q := m.queued; q != "" {
		m.queued = ""
		return m.send(q)
	}
	return nil
}

func (m *Model) onApprovalKey(key string) tea.Cmd {
	var a agent.Approval
	switch key {
	case "y", "enter":
		a = agent.Allow
	case "a":
		a = agent.AllowAlways
	case "n", "esc":
		a = agent.Deny
	case "ctrl+c":
		m.stop()
		return nil
	default:
		return nil
	}
	m.approval.Reply <- a
	m.approval = nil
	return nil
}

func (m *Model) toggleMode() {
	m.agentMode = !m.agentMode
	mode := m.mode()
	m.sess.Mode = mode.Name
	if m.agentMode && mode.Tools.Len() == 0 {
		m.notice = "agent mode: no tools yet"
	}
}

func (m *Model) completions() []command {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \n") {
		return nil
	}
	var out []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, v) {
			out = append(out, c)
		}
	}
	return out
}
