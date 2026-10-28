MYPATH:=${PATH}
GOBIN := $(PWD)/bin
export PATH=$(GOBIN):${MYPATH}

install:
	curl https://dl.google.com/go/go1.25.0.darwin-arm64.tar.gz -o go.tar.gz
	tar -xvf go.tar.gz && rm go.tar.gz && mv go bin/go && rm -rf go

tools:
	curl https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | bash -s -- -b $(GOBIN)

lint:
	@echo "Linting..."
	go vet ./...

vendor:
	go mod tidy
	go mod vendor

build:
	@docker buildx build --platform linux/amd64 -t nortido/ripperr:latest .

build-arm:
	@docker buildx build --platform linux/arm64 -t ripperr:arm .

VERSION ?= $(shell git describe --tags --always --dirty)
IMAGE ?= nortido/ripperr
PLATFORMS ?= linux/amd64,linux/arm64
TAG := $(patsubst v%,%,$(VERSION))

# драйвер docker не собирает мультиархитектурный манифест, нужен builder docker-container
builder:
	@docker buildx inspect ripperr-multi >/dev/null 2>&1 || docker buildx create --name ripperr-multi --driver docker-container >/dev/null

build-multi: builder
	@mkdir -p dist
	@docker buildx build --builder ripperr-multi --platform $(PLATFORMS) -t $(IMAGE):$(TAG) --output type=oci,dest=dist/ripperr-$(TAG).tar .

push-multi: builder
	@test -z "$$(git status --porcelain)" || { echo "push-multi: uncommitted changes, commit first" >&2; exit 1; }
	@docker buildx build --builder ripperr-multi --platform $(PLATFORMS) -t $(IMAGE):$(TAG) -t $(IMAGE):latest --push .