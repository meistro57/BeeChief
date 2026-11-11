VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.6.0")
LDFLAGS := -s -w -X github.com/meistro57/beechief/internal/report.DefaultVersion=$(VERSION)

.PHONY: all build test vet release install clean

all: vet test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o beechief ./cmd/beechief

test:
	go test -race ./...

vet:
	go vet ./...

release:
	./scripts/build-release.sh "$(VERSION)"

install:
	./scripts/install.sh --from-source

clean:
	rm -rf beechief dist/
