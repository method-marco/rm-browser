# Build the AppLoad app for reMarkable.
#
#   make            -> dist/rm-browser-aarch64.tar.gz (Paper Pro / Move)
#                      dist/rm-browser-armv7.tar.gz   (reMarkable 1 / 2)
#   make docker     -> same, inside a container (needs only Docker)
#   make test       -> backend unit tests (includes the sandbox test)
#   make qmltest    -> frontend tests, headless Qt in Docker (screenshots in build/qmltest)
#   make install HOST=10.11.99.1 ARCH=aarch64 -> copy to a device over SSH

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
RCC     ?= rcc
APP      = rm-browser
GOFLAGS  = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
HOST    ?= 10.11.99.1
ARCH    ?= aarch64
GOIMAGE  = golang:1.26-bookworm

.PHONY: all test clean docker qmltest install aarch64 armv7

all: aarch64 armv7

build/resources.rcc: app/application.qrc app/ui/*.qml
	mkdir -p build
	$(RCC) --binary -o $@ app/application.qrc

build/%/backend/entry: backend/*.go backend/go.mod backend/go.sum
	mkdir -p $(dir $@)
	cd backend && CGO_ENABLED=0 GOOS=linux \
		$(if $(filter aarch64,$*),GOARCH=arm64,GOARCH=arm GOARM=7) \
		go build $(GOFLAGS) -o ../$@ .

aarch64 armv7: %: build/resources.rcc build/%/backend/entry
	rm -rf build/$*/$(APP) && mkdir -p build/$*/$(APP)/backend dist
	cp app/manifest.json app/icon.png build/resources.rcc build/$*/$(APP)/
	cp build/$*/backend/entry build/$*/$(APP)/backend/entry
	chmod 755 build/$*/$(APP)/backend/entry
	cp LICENSE build/$*/$(APP)/
	tar -C build/$* -czf dist/$(APP)-$*.tar.gz $(APP)

test:
	cd backend && go vet ./... && go test ./...

docker:
	docker run --rm -v "$(CURDIR)":/src -w /src -e VERSION=$(VERSION) $(GOIMAGE) \
		sh -c 'apt-get update -qq && apt-get install -y -qq qt6-base-dev-tools >/dev/null && \
		       git config --global --add safe.directory /src && \
		       make all test RCC=/usr/lib/qt6/libexec/rcc'

qmltest:
	docker build -q -t rm-browser-qmltest tests/qml
	mkdir -p build/qmltest
	docker run --rm -v "$(CURDIR)":/repo -w /repo -e QML_XHR_ALLOW_FILE_READ=1 rm-browser-qmltest sh -c \
		'/usr/lib/qt6/bin/qmltestrunner -import tests/qml/imports -input tests/qml/tst_browser.qml && \
		 if [ -f build/fixture/index.txt ]; then /usr/lib/qt6/bin/qmltestrunner -import tests/qml/imports -input tests/qml/tst_fixtures.qml; fi'

install: $(ARCH)
	ssh root@$(HOST) 'mkdir -p /home/root/xovi/exthome/appload'
	cat dist/$(APP)-$(ARCH).tar.gz | ssh root@$(HOST) 'tar -C /home/root/xovi/exthome/appload -xzf -'

clean:
	rm -rf build dist
