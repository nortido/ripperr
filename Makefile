MYPATH:=${PATH}
GOBIN := $(PWD)/bin
export PATH=$(GOBIN):${MYPATH}

install:
	curl https://dl.google.com/go/go1.20.14.darwin-arm64.tar.gz -o go.tar.gz
	tar -xvf go.tar.gz && rm go.tar.gz && mv go bin/go && rm -rf go

tools:
	curl https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | bash -s -- -b $(GOBIN)

lint:
	@echo "Linting..."
	go vet ./...

vendor:
	go mod tidy
	go mod vendor