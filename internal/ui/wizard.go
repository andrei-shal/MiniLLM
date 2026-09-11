package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"minillm/internal/config"
	"minillm/internal/llm"
)

// Setup adds an OpenAI-compatible provider: asks for the endpoint and key,
// offers the models from GET /models and saves the config.
func Setup(dark bool) (*config.Config, error) {
	th := newTheme(dark)
	in := bufio.NewReader(os.Stdin)
	existing, _ := config.Load()

	say := func(s string) { lipgloss.Println(s) }
	ask := func(label, def string) (string, error) {
		prompt := th.Accent.Render("? ") + th.Bold.Render(label)
		if def != "" {
			prompt += th.Faint.Render(" (" + def + ")")
		}
		fmt.Print(lipgloss.Sprint(prompt + " "))
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("setup interrupted")
		}
		if line = strings.TrimSpace(line); line == "" {
			line = def
		}
		return line, nil
	}

	say("")
	say(th.Accent.Bold(true).Render("◆ MiniLLM") + th.Muted.Render(" · provider setup"))
	say(th.Muted.Render("  Any OpenAI-compatible API works: llama.cpp, Ollama, vLLM, LM Studio, OpenRouter, LiteLLM…"))
	say("")

	for {
		rawURL, err := ask("Base URL", "http://localhost:8080/v1")
		if err != nil {
			return nil, err
		}
		baseURL := normalizeURL(rawURL)

		fmt.Print(lipgloss.Sprint(th.Accent.Render("? ") + th.Bold.Render("API key") + th.Faint.Render(" (Enter for none) ")))
		keyBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(string(keyBytes))

		name := guessName(baseURL)
		for i := 2; existing != nil && existing.Provider(name) != nil; i++ {
			name = fmt.Sprintf("%s-%d", guessName(baseURL), i)
		}
		for {
			if name, err = ask("Provider name", name); err != nil {
				return nil, err
			}
			if strings.Contains(name, "/") {
				say(th.Error.Render("  ✗ no \"/\" please"))
			} else if existing != nil && existing.Provider(name) != nil {
				say(th.Error.Render("  ✗ that provider already exists"))
			} else {
				break
			}
		}

		say(th.Muted.Render("  fetching " + baseURL + "/models…"))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		models, err := llm.New(baseURL, key, nil).ListModels(ctx)
		cancel()

		var model string
		if err == nil && len(models) > 0 {
			say(th.OK.Render("  ✓ ") + fmt.Sprintf("%d models", len(models)))
			if model, err = Pick("Model", models, dark, true); err != nil {
				return nil, err
			}
			if model == "" {
				return nil, errors.New("setup cancelled")
			}
		} else {
			if err != nil {
				say(th.Error.Render("  ✗ " + err.Error()))
			} else {
				say(th.Warn.Render("  the server returned no models"))
			}
			choice, err := ask("Re-enter the URL (r) or type a model name (m)?", "m")
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(strings.ToLower(choice), "r") {
				continue
			}
			if model, err = ask("Model", ""); err != nil || model == "" {
				return nil, errors.New("no model given")
			}
		}

		cfg, err := config.AddProvider(config.Provider{Name: name, BaseURL: baseURL, APIKey: key}, model)
		if err != nil {
			return nil, err
		}
		say(th.OK.Render("✓ ") + name + "/" + model + th.Muted.Render(" saved to "+config.Tilde(config.Path())))
		if key != "" {
			say(th.Faint.Render("  To keep the key out of the file, use an environment variable: api_key_env = \"MY_KEY\""))
		}
		say("")
		return cfg, nil
	}
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/chat/completions")
	return strings.TrimSuffix(s, "/models")
}

// guessName turns a URL into a short provider name: localhost → local,
// api.openrouter.ai → openrouter.
func guessName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "local"
	}
	host := u.Hostname()
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return "local"
	}
	labels := strings.Split(host, ".")
	if len(labels) >= 2 {
		if _, err := fmt.Sscan(labels[0], new(int)); err == nil {
			return "local" // an IP address
		}
		return labels[len(labels)-2]
	}
	return labels[0]
}
