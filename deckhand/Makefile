HANDLER ?= main
PACKAGE ?= $(HANDLER)
GOPATH  ?= $(HOME)/go
GOOS    ?= linux
GOARCH  ?= amd64
WORKDIR = $(CURDIR:$(GOPATH)%=/go%)
ifeq ($(WORKDIR),$(CURDIR))
	WORKDIR = /tmp
endif

all: build fetch-bootstrap pack

build:
	@echo "Building..."
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags='-w -s' -o $(HANDLER)

fetch-bootstrap:
	@echo "Fetching custom Go runtime bootstrap..."
	@LATEST_RELEASE=$(shell curl -s https://api.github.com/repos/aws/aws-lambda-go/releases/latest | jq -r .tag_name) && \
	curl -Lo bootstrap https://github.com/aws/aws-lambda-go/releases/download/$$LATEST_RELEASE/bootstrap && \
	chmod +x bootstrap

pack:
	@echo "Packing binary and bootstrap..."
	@zip $(PACKAGE).zip $(HANDLER) bootstrap

clean:
	@echo "Cleaning up..."
	@rm -rf $(HANDLER) $(PACKAGE).zip aws-lambda-go

.PHONY: all build fetch-bootstrap pack clean
