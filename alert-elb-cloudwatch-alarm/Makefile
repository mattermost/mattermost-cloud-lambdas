HANDLER ?= bootstrap
PACKAGE ?= $(HANDLER)
GOPATH  ?= $(HOME)/go
GOOS    ?= linux
GOARCH  ?= arm64
GOLANGCILINT_VER := v1.61.0

WORKDIR = $(CURDIR:$(GOPATH)%=/go%)
ifeq ($(WORKDIR),$(CURDIR))
	WORKDIR = /tmp
endif

all: build pack

build:
	@echo "Building..."
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags='-w -s' -o $(HANDLER) .

pack:
	@echo "Packing binary..."
	@zip $(PACKAGE).zip $(HANDLER)

clean:
	@echo "Cleaning up..."
	@rm -rf $(HANDLER) $(PACKAGE).zip

update-modules:
	go get -u ./...
	go mod tidy

## fmt: Run go fmt on codebase
fmt:
	@echo Checking if code is formatted
		files=$$(go list -f '{{range .GoFiles}}{{$$.Dir}}/{{.}} {{end}}' .); \
		if [ "$$files" ]; then \
			gofmt_output=$$(gofmt -d -s $$files 2>&1); \
			if [ "$$gofmt_output" ]; then \
				echo "$$gofmt_output"; \
				echo "gofmt failed"; \
				echo "To fix it, run:"; \
				echo "go fmt [FILE]"; \
				exit 1; \
			fi; \
		fi; \
	  echo "gofmt success"; \

## lint: Run golangci-lint on codebase
lint:
	@echo "Linting..."
	@if ! [ -x "$$(command -v golangci-lint)" ]; then \
		echo "golangci-lint is not installed. Please see https://github.com/golangci/golangci-lint#install for installation instructions."; \
		exit 1; \
	fi; \

	@echo Running golangci-lint
	golangci-lint run ./...

check-style: lint fmt

lint-install:
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCILINT_VER)

.PHONY: all build pack clean update-modules lint fmt check-style lint-install
