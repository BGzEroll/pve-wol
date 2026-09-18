PREFIX ?= /usr/local

.PHONY: build install clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o pve-wol ./cmd/pve-wol

install: build
	install -Dm755 pve-wol $(DESTDIR)$(PREFIX)/bin/pve-wol

clean:
	rm -f pve-wol
