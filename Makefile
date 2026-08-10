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
	@docker buildx build --platform linux/amd64 -t nortido/trackerr:latest .

build-arm:
	@docker buildx build --platform linux/arm64 -t trackerr:arm .