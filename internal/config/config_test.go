package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	c := &Config{
		Default: "local/qwen",
		Providers: []Provider{
			{Name: "local", BaseURL: "x"},
			{Name: "or", BaseURL: "y", Models: []string{"meta/llama-3"}},
		},
	}
	cases := map[string]string{
		"":             "local/qwen",
		"local":        "local/qwen",
		"or":           "or/meta/llama-3",
		"or/gpt":       "or/gpt",
		"meta/llama-3": "or/meta/llama-3",
		"other/model":  "local/other/model",
		"mistral":      "local/mistral",
	}
	for ref, want := range cases {
		p, m, err := c.Resolve(ref)
		if err != nil || p.Name+"/"+m != want {
			t.Errorf("Resolve(%q) = %v/%v, %v; want %s", ref, p.Name, m, err, want)
		}
	}
}

func TestAddProviderKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("MINILLM_CONFIG", path)

	c, err := AddProvider(Provider{Name: "local", BaseURL: "http://localhost:8080/v1"}, "qwen")
	if err != nil || c.Default != "local/qwen" || len(c.Providers) != 1 {
		t.Fatalf("first add: %+v %v", c, err)
	}
	c, err = AddProvider(Provider{Name: "or", BaseURL: "https://openrouter.ai/api/v1", APIKey: `k"ey`}, "a/b")
	if err != nil || c.Default != "or/a/b" || len(c.Providers) != 2 || c.Providers[1].APIKey != `k"ey` {
		t.Fatalf("second add: %+v %v", c, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# system =") {
		t.Fatalf("comments lost:\n%s", raw)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("config must be private, got %v", info.Mode())
	}
}
