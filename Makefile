BIN     := mllm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

export CGO_ENABLED=0

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/$(BIN)

test:
	go vet ./...
	go test ./...

dist:
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "→ dist/$(BIN)-$$os-$$arch$$ext"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-$$os-$$arch$$ext . || exit 1; \
	done

clean:
	rm -rf $(BIN) dist

.PHONY: build install test dist clean
