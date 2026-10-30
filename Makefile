VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet run demo release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/xrayscope ./cmd/xrayscope

test:
	go test ./...

vet:
	go vet ./...

# Serve the UI from disk for quick front-end work.
run:
	XRAYSCOPE_WEB_DIR=internal/server/web go run ./cmd/xrayscope serve -c .demo/config.json

# Fill .demo with synthetic data (login: admin / demo12345).
demo:
	go run ./cmd/xrayscope-demo -data .demo
	echo '{"listen":"127.0.0.1:8686","data_dir":".demo","log_path":"","panel":{"type":"none"}}' > .demo/config.json

release:
	rm -rf dist && mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/xrayscope ./cmd/xrayscope && \
	  tar -czf dist/xrayscope-linux-$$arch.tar.gz -C dist xrayscope && \
	  (cd dist && sha256sum xrayscope-linux-$$arch.tar.gz > xrayscope-linux-$$arch.tar.gz.sha256) && rm dist/xrayscope; \
	done

clean:
	rm -rf bin dist
