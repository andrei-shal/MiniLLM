// Package config loads ~/.config/minillm/config.toml.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Provider struct {
	Name      string            `toml:"name"`
	BaseURL   string            `toml:"base_url"`
	APIKey    string            `toml:"api_key,omitempty"`
	APIKeyEnv string            `toml:"api_key_env,omitempty"`
	Headers   map[string]string `toml:"headers,omitempty"`
	Models    []string          `toml:"models,omitempty"`
}

// Key returns the API key, preferring the environment variable if it is set.
func (p *Provider) Key() string {
	if p.APIKeyEnv != "" {
		if v := os.Getenv(p.APIKeyEnv); v != "" {
			return v
		}
	}
	return p.APIKey
}

type Chat struct {
	System      string   `toml:"system,omitempty"`
	Temperature *float64 `toml:"temperature,omitempty"`
	MaxTokens   int      `toml:"max_tokens,omitempty"`
}

type Config struct {
	Default   string     `toml:"default"`
	Theme     string     `toml:"theme,omitempty"` // auto (default), dark, light
	Providers []Provider `toml:"provider"`
	Chat      Chat       `toml:"chat"`
}

var ErrNotFound = errors.New("config not found")

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Dir is $XDG_CONFIG_HOME/minillm, ~/.config/minillm by default (also on macOS,
// like most CLI tools).
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "minillm")
	}
	if runtime.GOOS == "windows" {
		d, _ := os.UserConfigDir()
		return filepath.Join(d, "minillm")
	}
	return filepath.Join(home(), ".config", "minillm")
}

// Path can be overridden with MINILLM_CONFIG.
func Path() string {
	if p := os.Getenv("MINILLM_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Dir(), "config.toml")
}

// DataDir holds sessions and input history.
func DataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "minillm")
	}
	if runtime.GOOS == "windows" {
		d, _ := os.UserConfigDir()
		return filepath.Join(d, "minillm", "data")
	}
	return filepath.Join(home(), ".local", "share", "minillm")
}

// Tilde shortens a path under the home directory to ~/….
func Tilde(p string) string {
	if h := home(); h != "" && (p == h || strings.HasPrefix(p, h+string(filepath.Separator))) {
		return "~" + p[len(h):]
	}
	return p
}

func Load() (*Config, error) {
	raw, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", Tilde(Path()), err)
	}
	if len(c.Providers) == 0 {
		return nil, fmt.Errorf("%s: no [[provider]] defined", Tilde(Path()))
	}
	switch c.Theme {
	case "", "auto", "dark", "light":
	default:
		return nil, fmt.Errorf("%s: theme = %q, expected auto, dark or light", Tilde(Path()), c.Theme)
	}
	seen := map[string]bool{}
	for i, p := range c.Providers {
		if p.Name == "" || strings.Contains(p.Name, "/") {
			return nil, fmt.Errorf("%s: provider #%d: needs a name without \"/\"", Tilde(Path()), i+1)
		}
		if p.BaseURL == "" {
			return nil, fmt.Errorf("%s: provider %q: base_url is not set", Tilde(Path()), p.Name)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("%s: provider %q is defined twice", Tilde(Path()), p.Name)
		}
		seen[p.Name] = true
	}
	return &c, nil
}

func (c *Config) Provider(name string) *Provider {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// Resolve turns "provider/model", a bare provider name, or a bare model (which
// may itself contain slashes) into a provider and a model. Empty means default.
func (c *Config) Resolve(ref string) (*Provider, string, error) {
	if ref == "" {
		ref = c.Default
	}
	if ref == "" {
		p := &c.Providers[0]
		if len(p.Models) == 0 {
			return nil, "", errors.New("no model selected: set default in the config or pass -m")
		}
		return p, p.Models[0], nil
	}
	if p := c.Provider(ref); p != nil {
		if def, model, ok := strings.Cut(c.Default, "/"); ok && def == p.Name {
			return p, model, nil
		}
		if len(p.Models) > 0 {
			return p, p.Models[0], nil
		}
		return nil, "", fmt.Errorf("provider %q has no model: use -m %s/<model>", p.Name, p.Name)
	}
	if name, model, ok := strings.Cut(ref, "/"); ok && model != "" {
		if p := c.Provider(name); p != nil {
			return p, model, nil
		}
	}
	for i := range c.Providers {
		if slices.Contains(c.Providers[i].Models, ref) {
			return &c.Providers[i], ref, nil
		}
	}
	// An unknown bare model goes to the default provider.
	p := &c.Providers[0]
	if name, _, ok := strings.Cut(c.Default, "/"); ok && c.Provider(name) != nil {
		p = c.Provider(name)
	}
	return p, ref, nil
}

// AddProvider appends a provider to the config file (creating it if needed)
// and makes provider/model the default. The existing file is edited as text so
// the user's comments survive.
func AddProvider(p Provider, model string) (*Config, error) {
	raw, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		raw, err = []byte(template), nil
	}
	if err != nil {
		return nil, err
	}
	text := string(raw)
	def := fmt.Sprintf("default = %s", quote(p.Name+"/"+model))
	defRe := regexp.MustCompile(`(?m)^default\s*=.*$`)
	if defRe.MatchString(text) {
		text = defRe.ReplaceAllLiteralString(text, def)
	} else {
		text = def + "\n" + text
	}
	block := fmt.Sprintf("\n[[provider]]\nname = %s\nbase_url = %s\n", quote(p.Name), quote(p.BaseURL))
	if p.APIKey != "" {
		block += fmt.Sprintf("api_key = %s          # or api_key_env = \"MY_KEY\"\n", quote(p.APIKey))
	} else {
		block += "# api_key = \"...\"              # or api_key_env = \"MY_KEY\"\n"
	}
	block += "# headers = { \"X-Title\" = \"minillm\" }\n# models = [\"model-a\", \"model-b\"]  # if the server doesn't serve /models\n"
	text = strings.TrimRight(text, "\n") + "\n" + block

	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return nil, err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		return nil, err
	}
	return Load()
}

const template = `# MiniLLM. Default model: "provider/model".
default = ""
# theme = "auto"   # auto | dark | light

[chat]
# system = "You are a helpful assistant."
# temperature = 0.7
# max_tokens = 4096
`

// quote returns a TOML basic string (JSON escapes are valid TOML).
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
