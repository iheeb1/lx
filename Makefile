VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/iheeb1/lx/internal/cli.Version=$(VERSION)

.PHONY: build install test golden vet bench charts clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o lx ./cmd/lx

install:
	CGO_ENABLED=0 go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/lx

test:
	go test ./...

# Rewrite golden files after an intentional output change; review the diff.
golden:
	LX_UPDATE_GOLDEN=1 go test ./...

vet:
	gofmt -l . | (! grep .) && go vet ./...

# Token savings over the real-output corpus (writes bench/out/results.json).
bench:
	go run ./bench/cmd/corpusbench -out bench/out/results.json

charts: bench
	go run ./bench/cmd/charts -in bench/out/results.json -out docs/img

clean:
	rm -rf lx dist bench/out
