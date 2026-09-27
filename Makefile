VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/iheeb1/lx/internal/cli.Version=$(VERSION)
# Release targets. Archive names carry no version, so
# https://github.com/iheeb1/lx/releases/latest/download/<name> always works.
PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
SHA256 = $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)
# Archive entries owned by 0:0 rather than by whoever ran make, and no xattrs
# (macOS adds com.apple.provenance, which GNU tar warns about). GNU tar and
# bsdtar spell the owner flags differently.
TARFLAGS = --no-xattrs $(shell tar --version 2>/dev/null | grep -q GNU && echo --owner=0 --group=0 --numeric-owner || echo --uid 0 --gid 0 --numeric-owner)

.PHONY: build install test golden vet nonet bench charts dist install-test clean

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

# lx makes no network calls; keep it unable to: fail if a networking package is
# linked into lx for any OS we ship (with the release build's CGO_ENABLED=0).
# Checking that lx's own package is listed keeps a broken build from passing.
nonet:
	@set -e; for os in linux darwin windows; do \
		deps=$$(CGO_ENABLED=0 GOOS=$$os go list -deps ./cmd/lx); \
		printf '%s\n' "$$deps" | grep -qx github.com/iheeb1/lx/internal/cli || \
			{ echo "nonet: go list did not list lx's packages for $$os"; exit 1; }; \
		if printf '%s\n' "$$deps" | grep -xE 'net|net/http|crypto/tls|net/rpc|net/smtp|net/mail'; then \
			echo "nonet: lx for $$os links the networking package(s) above"; exit 1; fi; \
	done; echo 'nonet: ok'

# Token savings over the real-output corpus (writes bench/out/results.json).
bench:
	go run ./bench/cmd/corpusbench -out bench/out/results.json

charts: bench
	go run ./bench/cmd/charts -in bench/out/results.json -out docs/img

# Release assets: dist/lx_<os>_<arch>.tar.gz (.zip for windows), each holding
# lx, LICENSE and README.md; dist/install.sh; and dist/SHA256SUMS covering all
# of them (so the release's provenance attestation covers install.sh too).
# Static, trimmed binaries.
dist:
	rm -rf dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; stage=dist/stage/$${os}_$${arch}; exe=lx; \
		if [ "$$os" = windows ]; then exe=lx.exe; fi; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o $$stage/$$exe ./cmd/lx; \
		cp LICENSE README.md $$stage/; \
		chmod 0755 $$stage/$$exe; chmod 0644 $$stage/LICENSE $$stage/README.md; \
		if [ "$$os" = windows ]; then \
			(cd $$stage && zip -q -X ../../lx_$${os}_$${arch}.zip $$exe LICENSE README.md); \
		else \
			COPYFILE_DISABLE=1 tar $(TARFLAGS) -czf dist/lx_$${os}_$${arch}.tar.gz -C $$stage $$exe LICENSE README.md; \
		fi; \
	done
	cp install.sh dist/install.sh
	cd dist && $(SHA256) lx_* install.sh > SHA256SUMS && $(SHA256) -c SHA256SUMS

# End-to-end check of install.sh against a host-only `make dist`, over file://
# (no network): it installs a working lx, and a corrupted archive installs nothing.
install-test:
	$(MAKE) dist PLATFORMS=$$(go env GOOS)/$$(go env GOARCH) VERSION=v0.0.0-installtest
	@set -e; t=$$(mktemp -d); trap 'rm -rf "$$t"' EXIT; \
	LX_BASE_URL="file://$(CURDIR)/dist" LX_INSTALL_DIR="$$t/ok" sh dist/install.sh; \
	"$$t/ok/lx" version | grep -F 'lx v0.0.0-installtest ('; \
	test -z "$$(find "$$t/ok" -name '.lx.new.*')"; \
	cp -R dist "$$t/bad-dist"; \
	for a in "$$t"/bad-dist/lx_*.tar.gz; do printf x >> "$$a"; done; \
	if LX_BASE_URL="file://$$t/bad-dist" LX_INSTALL_DIR="$$t/bad" sh dist/install.sh; then \
		echo 'install-test: install.sh accepted a corrupted archive'; exit 1; fi; \
	if [ -e "$$t/bad" ]; then echo 'install-test: a refused install left files behind'; exit 1; fi; \
	echo 'install-test: ok'

clean:
	rm -rf lx dist bench/out
