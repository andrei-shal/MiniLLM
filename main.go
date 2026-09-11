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

const usage = `mllm — минимальный клиент для OpenAI-совместимых LLM

  mllm                        интерактивный чат
  mllm "вопрос"               один ответ и выход
  cat file | mllm "что тут?"  stdin добавляется к вопросу
  mllm -c                     продолжить последний диалог

Флаги (пишутся до текста вопроса):
`

type flags struct {
	model, system          string
	cont, agent, raw, init bool
}

func main() {
	var f flags
	flag.StringVar(&f.model, "m", "", "модель: `провайдер/модель` или просто имя модели")
	flag.BoolVar(&f.cont, "c", false, "продолжить последний диалог")
	flag.BoolVar(&f.agent, "agent", false, "режим агента")
	flag.StringVar(&f.system, "s", "", "системный `промпт`")
	flag.BoolVar(&f.raw, "raw", false, "не рендерить markdown в ответе")
	flag.BoolVar(&f.init, "setup", false, "добавить провайдера (мастер настройки)")
	showVersion := flag.Bool("version", false, "показать версию")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nКонфиг: %s\n", config.Tilde(config.Path()))
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
		return errors.New("нет вопроса: передай его аргументом или через stdin")
	}

	// The interactive UI asks the terminal for its background asynchronously;
	// until then (and for one-shot output) guess from the environment.
	dark := guessDark()

	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) || (err == nil && f.init) {
		if !stdinTTY || !stdoutTTY {
			return fmt.Errorf("нет конфига %s — запусти mllm в терминале, чтобы настроить", config.Tilde(config.Path()))
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
			fmt.Fprintln(os.Stderr, "прошлых диалогов нет — начинаю новый")
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
