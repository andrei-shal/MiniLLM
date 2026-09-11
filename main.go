// mllm is a minimal terminal client for OpenAI-compatible LLM APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"minillm/internal/config"
	"minillm/internal/session"
	"minillm/internal/ui"
)

var version = "dev"

const usage = `mllm — a minimal client for OpenAI-compatible LLMs

  mllm                            interactive chat
  mllm "question"                 one answer, then exit
  cat file | mllm "what's this?"  stdin is appended to the question
  mllm -c                         continue the last conversation

Flags (they go before the question):
`

type flags struct {
	model, system          string
	cont, agent, raw, init bool
}

func main() {
	var f flags
	flag.StringVar(&f.model, "m", "", "model: `provider/model` or just a model name")
	flag.BoolVar(&f.cont, "c", false, "continue the last conversation")
	flag.BoolVar(&f.agent, "agent", false, "agent mode")
	flag.StringVar(&f.system, "s", "", "system `prompt`")
	flag.BoolVar(&f.raw, "raw", false, "don't render markdown in the answer")
	flag.BoolVar(&f.init, "setup", false, "add a provider (setup wizard)")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nConfig: %s\n", config.Tilde(config.Path()))
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("mllm", version)
		return
	}
	if err := run(f, flag.Args()); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "mllm:", err)
		os.Exit(1)
	}
}

func run(f flags, args []string) error {
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))

	prompt := strings.TrimSpace(strings.Join(args, " "))
	if !stdinTTY && stdinHasData() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if piped := strings.TrimSpace(string(data)); piped != "" {
			if prompt == "" {
				prompt = piped
			} else {
				prompt += "\n\n" + piped
			}
		}
	}
	interactive := prompt == ""
	if interactive && !stdinTTY {
		return errors.New("no question: pass it as an argument or via stdin")
	}

	// The interactive UI asks the terminal for its background asynchronously;
	// until then (and for one-shot output) guess from the environment.
	dark := guessDark()

	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) || (err == nil && f.init) {
		if !stdinTTY || !stdoutTTY {
			return fmt.Errorf("no config at %s — run mllm in a terminal to set it up", config.Tilde(config.Path()))
		}
		cfg, err = ui.Setup(dark)
	}
	if err != nil {
		return err
	}
	switch cfg.Theme {
	case "dark":
		dark = true
	case "light":
		dark = false
	}

	var sess *session.Session
	if f.cont {
		sess, err = session.Latest()
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "no earlier conversations — starting a new one")
		} else if err != nil {
			return err
		}
	}
	ref := f.model
	if ref == "" && sess != nil {
		// Only reuse the session's model if its provider still exists.
		if name, _, _ := strings.Cut(sess.Model, "/"); cfg.Provider(name) != nil {
			ref = sess.Model
		}
	}
	prov, model, err := cfg.Resolve(ref)
	if err != nil {
		return err
	}
	if sess == nil {
		sess = session.New()
		sess.System = cfg.Chat.System
	}
	if f.system != "" {
		sess.System = f.system
	}
	sess.Model = prov.Name + "/" + model

	opts := ui.Options{
		Config:      cfg,
		Session:     sess,
		Provider:    prov,
		Model:       model,
		Agent:       f.agent || sess.Mode == "agent",
		Dark:        dark,
		DetectTheme: cfg.Theme == "" || cfg.Theme == "auto",
		Version:     version,
	}
	if !interactive {
		return ui.OneShot(opts, prompt, f.raw || !stdoutTTY)
	}
	return ui.Run(opts)
}

// stdinHasData reports whether stdin is a pipe or a file. Other non-terminal
// stdins (sockets from ssh, IDE runners) may never reach EOF.
func stdinHasData() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeNamedPipe != 0 || fi.Mode().IsRegular())
}

// guessDark reads COLORFGBG ("fg;bg", set by many terminals); dark otherwise.
func guessDark() bool {
	if v := os.Getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return bg < 7 || bg == 8
		}
	}
	return true
}
