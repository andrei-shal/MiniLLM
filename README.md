# MiniLLM

A minimal terminal chat client for any OpenAI-compatible LLM: llama.cpp, Ollama, vLLM, LM Studio, OpenRouter, LiteLLM, or anything else that speaks `/v1/chat/completions`.

It is a single static `mllm` binary with no runtime or dependencies, written in Go on top of [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Glamour](https://github.com/charmbracelet/glamour).

```
◆ MiniLLM
  /help for commands · ⇧⇥ chat/agent · esc to stop

❯ how do I reverse a slice in Go?

  Since Go 1.21 the simplest way is slices.Reverse:

    s := []int{1, 2, 3}
    slices.Reverse(s) // [3 2 1]

╭──────────────────────────────────────────────────────────────╮
│ ❯ Ask anything…  (/ for commands)                            │
╰──────────────────────────────────────────────────────────────╯
 ● local/qwen3-32b  chat  ctx 1.2k    / commands · ⇧⇥ mode
```

## Features

- **Inline UI, like Claude Code.** Answers go into the terminal's normal scrollback, so scrolling, selecting and copying work as usual. Only the input box and a status line (model, mode, context used) stay at the bottom.
- **Streaming markdown.** Headings, lists, tables and syntax-highlighted code. Finished blocks are printed right away; the block being written is shown live.
- **Model reasoning** (`reasoning_content`, `reasoning`, or `<think>…</think>`) is folded into a single `✻ thought for 3.2s` line; `/think` expands it.
- **Any number of custom providers** in one config. Switch models on the fly, even mid-conversation.
- **Saved conversations.** `mllm -c` continues the last one; `/sessions` opens any earlier one.
- **One-shot and pipes:** `mllm "question"` or `cat file | mllm "explain"`. When stdout isn't a terminal, the answer is printed as plain text.
- **First-run setup wizard.** It asks for the endpoint and key, lists the server's models, and writes the config.
- **Ready for an agent mode.** Chat and agent are the same loop, and Shift+Tab switches between them (see [below](#agent-mode)).

## Install

### Prebuilt binary

Download the binary for your platform from [Releases](https://github.com/andrei-shal/MiniLLM/releases/latest): `mllm-linux-amd64`, `mllm-linux-arm64`, `mllm-darwin-amd64`, `mllm-darwin-arm64`, or `mllm-windows-amd64.exe`. For example, on Linux:

```sh
curl -L -o ~/.local/bin/mllm https://github.com/andrei-shal/MiniLLM/releases/latest/download/mllm-linux-amd64
chmod +x ~/.local/bin/mllm
```

The macOS binaries are not signed. If Gatekeeper blocks one, remove the quarantine flag with `xattr -d com.apple.quarantine mllm`. SHA-256 checksums are in `checksums.txt` next to the binaries.

### From source

Requires Go 1.27+.

```sh
git clone https://github.com/andrei-shal/MiniLLM.git
cd MiniLLM
make install      # builds and installs to ~/.local/bin/mllm
```

Other targets:

```sh
make build        # ./mllm
make dist         # linux/darwin/windows × amd64/arm64 → dist/
make test         # go vet + go test
```

## Quick start

```sh
mllm
```

On first run the wizard asks for a base URL (e.g. `http://localhost:8080/v1`) and an API key, fetches `GET /models`, and lets you pick a model. The config is saved to `~/.config/minillm/config.toml`. Add more providers later with `mllm -setup`.

## Usage

```sh
mllm                            # interactive chat
mllm "how are you?"             # one answer, then exit
cat main.go | mllm "what's this?"   # stdin is appended to the question
mllm -c                         # continue the last conversation
mllm -m local/qwen3 "hi"        # pick a model
mllm -s "Be brief" "…"          # system prompt
mllm -raw "…" > answer.md       # no formatting
```

Flags go **before** the question. See `mllm -h` for the full list.

### Keys

| Keys | Action |
|---|---|
| `Enter` | send |
| `Alt+Enter`, `Shift+Enter`, `Ctrl+J`, `\` + `Enter` | new line |
| `Esc`, `Ctrl+C` | stop the answer |
| `↑` `↓` | input history |
| `Tab` | complete a command |
| `Shift+Tab` | switch chat ↔ agent |
| `Ctrl+L` | clear the screen |
| `Ctrl+C` twice, `Ctrl+D` | quit |

You can type your next message while the model is still answering; it is sent as soon as the answer is done.

### Commands

| Command | Action |
|---|---|
| `/model [name]` | pick a model (fuzzy picker) or switch to one by name |
| `/provider` | pick a provider, then one of its models |
| `/new` | start a new conversation |
| `/sessions` | open an earlier conversation |
| `/retry` | regenerate the last answer |
| `/system [text \| -]` | show, set or reset the system prompt |
| `/copy` | copy the last answer (OSC 52, works over ssh) |
| `/think` | show or hide model reasoning |
| `/agent` | switch chat ↔ agent |
| `/help` | help |
| `/exit` | quit |

## Configuration

The config lives at `~/.config/minillm/config.toml`; override the path with `MINILLM_CONFIG`.

```toml
default = "local/qwen3-32b"    # provider/model
# theme = "auto"               # auto | dark | light

[[provider]]
name = "local"
base_url = "http://localhost:8080/v1"
# models = ["qwen3-32b"]       # if the server doesn't serve /models

[[provider]]
name = "openrouter"
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"   # or api_key = "sk-…"
headers = { "X-Title" = "minillm" }

[chat]
system = "You are a helpful assistant."
# temperature = 0.7
# max_tokens = 4096
```

Models are written as `provider/model`. A bare model name also works: it resolves to the provider that lists that model, or to the default provider otherwise.

Conversations are stored in `~/.local/share/minillm/sessions/`, and input history in `~/.local/share/minillm/history`.

## Agent mode

A mode is just a system prompt plus a set of tools. Chat has no tools, so the model ↔ tools loop ends after the first answer. The agent is the same loop with tools, and both share the conversation, so you can start chatting and switch to the agent midway.

The plumbing is in place: the tool interface, the call loop, tool calls shown in the UI, and a `[y] yes [a] always [n] no` approval prompt. There are no tools yet. To add one, implement the interface and return it from `agentTools()` in `internal/agent/modes.go`:

```go
type Tool interface {
	Name() string
	Schema() llm.ToolDef                     // JSON Schema of the arguments
	NeedsApproval(args json.RawMessage) bool // ask the user first?
	Run(ctx context.Context, args json.RawMessage) (string, error)
}
```

## How it works

```
main.go               flags; interactive, one-shot or pipe
internal/llm          thin SSE client for /chat/completions and /models
internal/agent        model ↔ tools loop, events for the UI
internal/config       config file, adding providers
internal/session      conversations as JSON
internal/ui           Bubble Tea UI: input, streaming, pickers, commands, wizard
```

- **A hand-written HTTP client instead of an SDK,** because custom providers each have quirks. It understands `reasoning_content` and `reasoning`, strips `<think>`, assembles streamed `tool_calls` deltas, retries without `stream_options` when a server rejects it, and handles non-streaming responses.
- **The bottom area owns the latest conversation lines** and only hands them to the scrollback once they no longer fit. That's why menus and pickers don't scroll the terminal, and the input stays in place when they close.
- `internal/ui/frame.go` documents the workarounds for quirks of Bubble Tea v2.0.9's inline renderer and of tmux: the terminal cursor is kept parked, and prints are split into chunks.

## Releases

Pushing a `v*` tag triggers [`.github/workflows/release.yml`](.github/workflows/release.yml). It runs the tests, builds all platforms with `make dist`, and publishes a GitHub Release with the binaries, `checksums.txt`, and generated release notes:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

## Status

Early version. Tested on Linux with unit tests and scripted tmux sessions against a mock server. Binaries are about 16–17 MB, mostly because of syntax highlighting.
