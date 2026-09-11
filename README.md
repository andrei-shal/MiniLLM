# MiniLLM

Минимальный консольный клиент для любых OpenAI-совместимых LLM (llama.cpp, Ollama, vLLM, LM Studio, OpenRouter, LiteLLM…). Один статический бинарник `mllm`, без зависимостей.

## Сборка

```sh
make build        # ./mllm
make install      # ~/.local/bin/mllm
make dist         # linux/darwin/windows → dist/
```

## Использование

```sh
mllm                          # интерактивный чат
mllm "как дела?"              # один ответ и выход
cat main.go | mllm "что тут?" # stdin добавляется к вопросу
mllm -c                       # продолжить последний диалог
mllm -m local/qwen3 "привет"  # выбрать модель
mllm --setup                  # добавить ещё одного провайдера
```

Флаги пишутся **до** текста вопроса. Если stdout не терминал, ответ идёт сырым текстом, без оформления. Так удобно в пайпах.

При первом запуске мастер спросит адрес API и ключ, покажет список моделей и сохранит конфиг.

## Интерфейс

| Клавиши | Что делают |
|---|---|
| `Enter` | отправить |
| `Alt+Enter`, `Shift+Enter`, `Ctrl+J`, `\` + `Enter` | новая строка |
| `Esc`, `Ctrl+C` | остановить ответ |
| `↑` `↓` | история ввода |
| `Tab` | дополнить команду |
| `Shift+Tab` | переключить chat ↔ agent |
| `Ctrl+L` | очистить экран |
| `Ctrl+C` дважды, `Ctrl+D` | выход |

Команды: `/model`, `/provider`, `/new`, `/sessions`, `/retry`, `/system`, `/copy`, `/think`, `/agent`, `/help`, `/exit`.

Готовые блоки ответа уходят в обычную ленту терминала, поэтому скролл, выделение и копирование работают как обычно. Внизу живут только поле ввода и блок, который сейчас печатается. Рассуждения моделей (`reasoning_content`, `reasoning` или `<think>…</think>`) свёрнуты в одну строку; `/think` их показывает.

## Конфиг

`~/.config/minillm/config.toml` (путь можно переопределить через `MINILLM_CONFIG`):

```toml
default = "local/qwen3-32b"    # провайдер/модель
# theme = "auto"               # auto | dark | light

[[provider]]
name = "local"
base_url = "http://localhost:8080/v1"
# api_key = "..."              # или
# api_key_env = "LOCAL_KEY"    # ключ из переменной окружения
# headers = { "X-Title" = "minillm" }
# models = ["qwen3-32b"]       # если сервер не отдаёт /models

[[provider]]
name = "openrouter"
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"

[chat]
system = "Ты полезный ассистент."
# temperature = 0.7
# max_tokens = 4096
```

Диалоги хранятся в `~/.local/share/minillm/sessions/`, история ввода — в `~/.local/share/minillm/history`.

## Устройство

```
main.go               флаги, выбор режима: интерактив / one-shot / pipe
internal/llm          тонкий SSE-клиент /chat/completions и /models
internal/agent        цикл «модель ↔ инструменты», события для UI
internal/config       конфиг и мастер добавления провайдера
internal/session      диалоги в JSON
internal/ui           Bubble Tea: ввод, стриминг, пикеры, команды
```

Чат и агент — это один и тот же цикл `agent.Loop`. Режим (`agent.Mode`) — это просто системный промпт плюс набор инструментов. У чата инструментов нет, поэтому цикл заканчивается после первого ответа. Чтобы включить агента, реализуй интерфейс `agent.Tool` (bash, чтение и правка файлов…) и добавь инструменты в `agentTools()` в `internal/agent/modes.go`. UI уже умеет показывать вызовы инструментов и спрашивать подтверждение `[y/a/n]`.
