# Build the AppLoad apps for reMarkable: Browser (rm-browser) and Books
# (rm-books). They share the reader (app/shared) and backend code
# (backend/internal).
#
#   make            -> dist/rm-browser-aarch64.tar.gz, dist/rm-books-aarch64.tar.gz (Paper Pro / Move)
#                      dist/rm-browser-armv7.tar.gz,   dist/rm-books-armv7.tar.gz   (reMarkable 1 / 2)
#   make docker     -> same, inside a container (needs only Docker)
#   make test       -> backend unit tests (includes the sandbox test)
#   make qmltest    -> frontend tests, headless Qt in Docker (screenshots in build/qmltest)
#   make install HOST=10.11.99.1 ARCH=aarch64 [APPS=rm-books] -> copy to a device over SSH

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
RCC     ?= rcc
APPS    ?= rm-browser rm-books
GOFLAGS  = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
HOST    ?= 10.11.99.1
ARCH    ?= aarch64
GOIMAGE  = golang:1.26-bookworm

# app -> frontend folder (app/<dir>, app/<dir>.qrc) and backend package
dir_rm-browser = browser
dir_rm-books   = books
pkg_rm-browser = .
pkg_rm-books   = ./books
# third-party notices shipped with an app (files built into its backend)
notices_rm-books = backend/books/hyph/NOTICES.txt

SHARED_QML = app/shared/* app/fonts/*.ttf
GO_SRC     = $(shell find backend -name '*.go' -not -name '*_test.go') $(wildcard backend/books/hyph/*.txt) backend/go.mod backend/go.sum

.PHONY: all test clean docker qmltest install aarch64 armv7

all: aarch64 armv7

build/rm-browser.rcc: app/browser.qrc app/browser/*.qml $(SHARED_QML)
build/rm-books.rcc: app/books.qrc app/books/*.qml $(SHARED_QML)
build/%.rcc:
	mkdir -p build
	$(RCC) --binary -o $@ app/$(dir_$*).qrc

# build/<arch>/<app>.entry
build/aarch64/%.entry build/armv7/%.entry: $(GO_SRC)
	mkdir -p $(dir $@)
	cd backend && CGO_ENABLED=0 GOOS=linux \
		$(if $(findstring aarch64,$@),GOARCH=arm64,GOARCH=arm GOARM=7) \
		go build $(GOFLAGS) -o ../$@ $(pkg_$*)

# package <arch>,<app>: one shell line
package = rm -rf build/$(1)/$(2) && mkdir -p build/$(1)/$(2)/backend dist && \
	cp app/$(dir_$(2))/manifest.json app/$(dir_$(2))/icon.png build/$(1)/$(2)/ && \
	cp build/$(2).rcc build/$(1)/$(2)/resources.rcc && \
	cp build/$(1)/$(2).entry build/$(1)/$(2)/backend/entry && chmod 755 build/$(1)/$(2)/backend/entry && \
	cp LICENSE build/$(1)/$(2)/ && \
	$(if $(notices_$(2)),cp $(notices_$(2)) build/$(1)/$(2)/THIRD-PARTY-NOTICES.txt &&) tar -C build/$(1) -czf dist/$(2)-$(1).tar.gz $(2);

aarch64 armv7: %: $(foreach a,$(APPS),build/$(a).rcc build/%/$(a).entry)
	$(foreach a,$(APPS),$(call package,$*,$(a)))

test:
	cd backend && go vet ./... && go test ./...

docker:
	docker run --rm -v "$(CURDIR)":/src -w /src -e VERSION=$(VERSION) $(GOIMAGE) \
		sh -c 'apt-get update -qq && apt-get install -y -qq qt6-base-dev-tools >/dev/null && \
		       git config --global --add safe.directory /src && \
		       make all test RCC=/usr/lib/qt6/libexec/rcc'

QMLTEST = /usr/lib/qt6/bin/qmltestrunner -import tests/qml/imports -input
qmltest:
	docker build -q -t rm-browser-qmltest tests/qml
	docker build -q -t rm-browser-qmltest-latest -f tests/qml/Dockerfile.latest tests/qml
	mkdir -p build/qmltest/export
	docker run --rm -v "$(CURDIR)":/repo -w /repo -e QML_XHR_ALLOW_FILE_READ=1 -e QML_XHR_ALLOW_FILE_WRITE=1 rm-browser-qmltest sh -c \
		'$(QMLTEST) tests/qml/tst_browser.qml && $(QMLTEST) tests/qml/tst_books.qml && \
		 if [ -f build/fixture/index.txt ]; then $(QMLTEST) tests/qml/tst_fixtures.qml; fi'
	docker run --rm -v "$(CURDIR)":/repo -w /repo -e QML_XHR_ALLOW_FILE_READ=1 rm-browser-qmltest-latest sh -c \
		'$(QMLTEST) tests/qml/tst_browser.qml && $(QMLTEST) tests/qml/tst_books.qml'

install: $(ARCH)
	ssh root@$(HOST) 'mkdir -p /home/root/xovi/exthome/appload'
	$(foreach a,$(APPS),cat dist/$(a)-$(ARCH).tar.gz | ssh root@$(HOST) 'tar -C /home/root/xovi/exthome/appload -xzf -';)

clean:
	rm -rf build dist
