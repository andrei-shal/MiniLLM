package ui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"minillm/internal/config"
	"minillm/internal/llm"
	"minillm/internal/session"
)

type command struct {
	name, desc string
	idle       bool // not available while a response is streaming
	run        func(m *Model, arg string) tea.Cmd
}

var commands []command

// Filled in init: /help lists the commands, which would be an initialization cycle.
func init() {
	commands = []command{
		{"/model", "pick a model (or /model name)", true, (*Model).cmdModel},
		{"/provider", "pick a provider", true, (*Model).cmdProvider},
		{"/new", "new conversation", true, (*Model).cmdNew},
		{"/sessions", "open an earlier conversation", true, (*Model).cmdSessions},
		{"/retry", "regenerate the last answer", true, (*Model).cmdRetry},
		{"/system", "system prompt: /system text, /system - to reset", true, (*Model).cmdSystem},
		{"/copy", "copy the last answer", false, (*Model).cmdCopy},
		{"/think", "show model reasoning", false, (*Model).cmdThink},
		{"/agent", "switch chat ↔ agent", true, (*Model).cmdAgent},
		{"/help", "help", false, (*Model).cmdHelp},
		{"/exit", "quit", false, (*Model).cmdExit},
	}
}

func lookup(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

func (m *Model) note(s string) { m.print(m.th.Muted.Render("  ⎿ " + s)) }

func (m *Model) cmdModel(arg string) tea.Cmd {
	if arg != "" {
		return m.switchTo(m.qualify(arg, m.provider.Name))
	}
	return m.openModelPicker("")
}

func (m *Model) openModelPicker(only string) tea.Cmd {
	p := &picker{kind: "model", only: only, title: "Model"}
	if only != "" {
		p.title += " · " + only
	}
	var cmds []tea.Cmd
	for i := range m.cfg.Providers {
		prov := &m.cfg.Providers[i]
		if only != "" && prov.Name != only {
			continue
		}
		models := prov.Models
		if len(models) == 0 {
			models = m.modelsCache[prov.Name]
		}
		if len(models) == 0 {
			p.loading++
			cmds = append(cmds, fetchModels(prov))
		}
		p.addModels(prov.Name, models, m.ref())
	}
	prefix := m.provider.Name
	if only != "" {
		prefix = only
	}
	p.onPick = func(it pickItem) tea.Cmd { return m.switchTo(it.Value) }
	p.onCustom = func(text string) tea.Cmd { return m.switchTo(m.qualify(text, prefix)) }
	m.picker = p
	return tea.Batch(cmds...)
}

func fetchModels(p *config.Provider) tea.Cmd {
	client, name := llm.New(p.BaseURL, p.Key(), p.Headers), p.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		models, err := client.ListModels(ctx)
		return modelsMsg{provider: name, models: models, err: err}
	}
}

func (m *Model) onModels(msg modelsMsg) {
	if msg.err == nil {
		m.modelsCache[msg.provider] = msg.models
	}
	p := m.picker
	if p == nil || p.kind != "model" || (p.only != "" && p.only != msg.provider) {
		return
	}
	p.loading--
	if msg.err != nil {
		p.errs = append(p.errs, msg.provider+": "+msg.err.Error())
		return
	}
	p.addModels(msg.provider, msg.models, m.ref())
}

// qualify prefixes a bare model name with the provider that has it, or with
// the given provider.
func (m *Model) qualify(ref, provider string) string {
	if name, _, ok := strings.Cut(ref, "/"); ok && m.cfg.Provider(name) != nil {
		return ref
	}
	if m.cfg.Provider(ref) != nil {
		return ref
	}
	for _, p := range m.cfg.Providers {
		if slices.Contains(p.Models, ref) || slices.Contains(m.modelsCache[p.Name], ref) {
			return p.Name + "/" + ref
		}
	}
	return provider + "/" + ref
}

func (m *Model) switchTo(ref string) tea.Cmd {
	p, model, err := m.cfg.Resolve(ref)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	m.setModel(p, model)
	m.note("model: " + m.th.Accent.Render(m.ref()))
	return nil
}

func (m *Model) cmdProvider(string) tea.Cmd {
	p := &picker{kind: "provider", title: "Provider"}
	for _, prov := range m.cfg.Providers {
		it := pickItem{Label: prov.Name, Hint: prov.BaseURL, Value: prov.Name}
		if prov.Name == m.provider.Name {
			p.sel = len(p.items)
		}
		p.items = append(p.items, it)
	}
	p.onPick = func(it pickItem) tea.Cmd { return m.openModelPicker(it.Value) }
	m.picker = p
	return nil
}

func (m *Model) cmdNew(string) tea.Cmd {
	m.save()
	s := session.New()
	s.System = m.cfg.Chat.System
	m.sess = s
	m.setModel(m.provider, m.model)
	m.sess.Mode = m.mode().Name
	m.usage = llm.Usage{}
	m.print("\n" + m.th.Faint.Render("── new conversation ──"))
	return nil
}

func (m *Model) cmdSessions(string) tea.Cmd {
	metas, err := session.List(100)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	if len(metas) == 0 {
		m.notice = "no saved conversations yet"
		return nil
	}
	p := &picker{kind: "session", title: "Conversations"}
	for _, s := range metas {
		p.items = append(p.items, pickItem{Label: s.Title, Hint: relTime(s.Updated) + " · " + s.Model, Value: s.ID})
	}
	p.onPick = func(it pickItem) tea.Cmd { return m.openSession(it.Value) }
	m.picker = p
	return nil
}

func (m *Model) openSession(id string) tea.Cmd {
	s, err := session.Load(id)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	m.save()
	m.sess = s
	if p, model, err := m.cfg.Resolve(s.Model); err == nil && m.cfg.Provider(strings.SplitN(s.Model, "/", 2)[0]) != nil {
		m.setModel(p, model)
	} else {
		m.setModel(m.provider, m.model)
	}
	m.agentMode = s.Mode == "agent"
	m.usage = llm.Usage{}
	m.print("\n" + m.th.Faint.Render("── "+s.Title+" ──"))
	m.print(m.transcript(s.Messages))
	return nil
}

func (m *Model) cmdRetry(string) tea.Cmd {
	i := len(m.sess.Messages) - 1
	for i >= 0 && m.sess.Messages[i].Role != llm.RoleUser {
		i--
	}
	if i < 0 {
		m.notice = "nothing to retry"
		return nil
	}
	m.sess.Messages = m.sess.Messages[:i+1]
	m.print("\n" + m.th.Muted.Render("↻ retry"))
	return m.startTurn()
}

func (m *Model) cmdSystem(arg string) tea.Cmd {
	switch arg {
	case "":
		sys := m.sess.System
		if sys == "" {
			sys = "not set"
		}
		m.note("system prompt: " + sys)
	case "-":
		m.sess.System = m.cfg.Chat.System
		m.note("system prompt reset")
	default:
		m.sess.System = arg
		m.note("system prompt updated")
	}
	return nil
}

func (m *Model) cmdCopy(string) tea.Cmd {
	for i := len(m.sess.Messages) - 1; i >= 0; i-- {
		if msg := m.sess.Messages[i]; msg.Role == llm.RoleAssistant && msg.Content != "" {
			m.notice = "copied to clipboard"
			return tea.SetClipboard(msg.Content)
		}
	}
	m.notice = "no answer yet"
	return nil
}

func (m *Model) cmdThink(string) tea.Cmd {
	m.showThink = !m.showThink
	if !m.showThink {
		m.notice = "reasoning hidden"
		return nil
	}
	m.notice = "reasoning shown"
	for i := len(m.sess.Messages) - 1; i >= 0; i-- {
		if msg := m.sess.Messages[i]; msg.Role == llm.RoleAssistant {
			if msg.Reasoning != "" {
				m.print("\n" + m.thinkingBlock(msg.Reasoning, 0))
			}
			break
		}
	}
	return nil
}

func (m *Model) cmdAgent(string) tea.Cmd {
	m.toggleMode()
	if m.notice == "" {
		m.notice = "mode: " + m.mode().Name
	}
	return nil
}

func (m *Model) cmdHelp(string) tea.Cmd {
	var b strings.Builder
	b.WriteString(m.th.Bold.Render("Commands") + "\n")
	for _, c := range commands {
		b.WriteString("  " + m.th.Accent.Render(padRight(c.name, 11)) + m.th.Muted.Render(c.desc) + "\n")
	}
	b.WriteString("\n" + m.th.Bold.Render("Keys") + "\n")
	for _, k := range [][2]string{
		{"⏎", "send"},
		{"⌥⏎ ⇧⏎ ^J \\⏎", "new line"},
		{"esc, ^C", "stop the answer"},
		{"↑ ↓", "input history"},
		{"tab", "complete a command"},
		{"⇧⇥", "switch chat ↔ agent"},
		{"^L", "clear the screen"},
		{"^C ^C, ^D", "quit"},
	} {
		b.WriteString("  " + m.th.Accent.Render(padRight(k[0], 11)) + m.th.Muted.Render(k[1]) + "\n")
	}
	b.WriteString("\n" + m.th.Muted.Render("Config: "+config.Tilde(config.Path())))
	m.print("\n" + b.String())
	return nil
}

func (m *Model) cmdExit(string) tea.Cmd { return m.quit() }

func padRight(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s + " "
}
