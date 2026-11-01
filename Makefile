# winslot build. `make check` before every commit; `make deb` for the package.
#
#   make build        bin/winslot (static, no cgo)
#   make test         unit tests
#   make check        vet + staticcheck + unit tests with the race detector
#   make cover        coverage report (bin/coverage.html)
#   make integration  build the libvirt integration test binary; run it as root
#                     on a winslot host: sudo bin/virt.itest -test.v
#   make deb          ../winslot_<version>_amd64.deb (debian/rules calls build/install)
#   make installer SITE=pnc WIN_ISO=...   server installer ISO (install/make-installer)

VERSION     ?= $(shell dpkg-parsechangelog -S Version 2>/dev/null || echo dev)
GO          ?= go
STATICCHECK ?= $(GO) run honnef.co/go/tools/cmd/staticcheck@2025.1.1
PREFIX      ?= /usr
LDFLAGS     := -s -w -X winslot/internal/cli.Version=$(VERSION)

.PHONY: all build test check fmt vet lint cover integration completions install deb vendor clean installer

all: check build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/winslot ./cmd/winslot

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
	$(GO) vet -tags libvirt ./...

lint:
	$(STATICCHECK) ./...
	$(STATICCHECK) -tags libvirt ./...

check: fmt vet lint
	$(GO) test -race ./...

fmt:
	@test -z "$$(gofmt -l cmd internal assets)" || { gofmt -l cmd internal assets; echo "gofmt needed"; exit 1; }

cover:
	$(GO) test -coverprofile=bin/coverage.out ./...
	$(GO) tool cover -html=bin/coverage.out -o bin/coverage.html
	$(GO) tool cover -func=bin/coverage.out | tail -1

integration:
	$(GO) test -c -tags libvirt -o bin/virt.itest ./internal/virt

completions: build
	bin/winslot completion bash > bin/winslot.bash
	bin/winslot completion zsh  > bin/_winslot
	bin/winslot completion fish > bin/winslot.fish

install: build completions
	install -D -m 755 bin/winslot           $(DESTDIR)$(PREFIX)/sbin/winslot
	install -D -m 644 bin/winslot.bash      $(DESTDIR)$(PREFIX)/share/bash-completion/completions/winslot
	install -D -m 644 bin/_winslot          $(DESTDIR)$(PREFIX)/share/zsh/vendor-completions/_winslot
	install -D -m 644 bin/winslot.fish      $(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d/winslot.fish
	install -D -m 644 systemd/winslot-license-scan.service $(DESTDIR)$(PREFIX)/lib/systemd/system/winslot-license-scan.service
	install -D -m 644 systemd/winslot-license-scan.timer   $(DESTDIR)$(PREFIX)/lib/systemd/system/winslot-license-scan.timer
	install -D -m 644 systemd/winslot-console.service      $(DESTDIR)$(PREFIX)/lib/systemd/system/winslot-console.service
	install -d -m 755 $(DESTDIR)/etc/winslot
	install -D -m 644 etc/winslot.toml      $(DESTDIR)$(PREFIX)/share/doc/winslot/winslot.toml.example
	install -D -m 644 README.md             $(DESTDIR)$(PREFIX)/share/doc/winslot/README.md
	install -D -m 644 RUNBOOK.md            $(DESTDIR)$(PREFIX)/share/doc/winslot/RUNBOOK.md
	install -D -m 644 install/root-README   $(DESTDIR)$(PREFIX)/share/doc/winslot/root-README

deb:
	dpkg-buildpackage -b -us -uc

vendor:
	$(GO) mod tidy
	$(GO) mod vendor

clean:
	rm -rf bin

installer:
	install/make-installer $(SITE) $(WIN_ISO) $(or $(PAYLOAD),/var/cache/winslot/payload) winslot-installer-$(SITE).iso $(CA_BACKUP)
