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
			return "", errors.New("настройка прервана")
		}
		if line = strings.TrimSpace(line); line == "" {
			line = def
		}
		return line, nil
	}

	say("")
	say(th.Accent.Bold(true).Render("◆ MiniLLM") + th.Muted.Render(" · настройка провайдера"))
	say(th.Muted.Render("  Подойдёт любой OpenAI-совместимый API: llama.cpp, Ollama, vLLM, LM Studio, OpenRouter, LiteLLM…"))
	say("")

	for {
		rawURL, err := ask("Base URL", "http://localhost:8080/v1")
		if err != nil {
			return nil, err
		}
		baseURL := normalizeURL(rawURL)

		fmt.Print(lipgloss.Sprint(th.Accent.Render("? ") + th.Bold.Render("API ключ") + th.Faint.Render(" (Enter — без ключа) ")))
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
			if name, err = ask("Имя провайдера", name); err != nil {
				return nil, err
			}
			if strings.Contains(name, "/") {
				say(th.Error.Render("  ✗ без «/», пожалуйста"))
			} else if existing != nil && existing.Provider(name) != nil {
				say(th.Error.Render("  ✗ такой провайдер уже есть"))
			} else {
				break
			}
		}

		say(th.Muted.Render("  запрашиваю " + baseURL + "/models…"))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		models, err := llm.New(baseURL, key, nil).ListModels(ctx)
		cancel()

		var model string
		if err == nil && len(models) > 0 {
			say(th.OK.Render("  ✓ ") + fmt.Sprintf("моделей: %d", len(models)))
			if model, err = Pick("Модель", models, dark, true); err != nil {
				return nil, err
			}
			if model == "" {
				return nil, errors.New("настройка отменена")
			}
		} else {
			if err != nil {
				say(th.Error.Render("  ✗ " + err.Error()))
			} else {
				say(th.Warn.Render("  сервер вернул пустой список моделей"))
			}
			choice, err := ask("Ввести адрес заново (r) или указать модель вручную (m)?", "m")
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(strings.ToLower(choice), "r") {
				continue
			}
			if model, err = ask("Модель", ""); err != nil || model == "" {
				return nil, errors.New("модель не указана")
			}
		}

		cfg, err := config.AddProvider(config.Provider{Name: name, BaseURL: baseURL, APIKey: key}, model)
		if err != nil {
			return nil, err
		}
		say(th.OK.Render("✓ ") + name + "/" + model + th.Muted.Render(" сохранено в "+config.Tilde(config.Path())))
		if key != "" {
			say(th.Faint.Render("  Ключ можно убрать из файла в переменную окружения: api_key_env = \"MY_KEY\""))
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
