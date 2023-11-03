HANDLER ?= bootstrap
PACKAGE ?= $(HANDLER)
GOOS    ?= linux
GOARCH  ?= amd64

export GO111MODULE=on

all: build pack 

build:
	@echo "Building..."
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags='-w -s' -o $(HANDLER) .

pack: build
	@echo "Packing binary..."
	@zip $(PACKAGE).zip $(HANDLER)

clean:
	@echo "Cleaning up..."
	@rm -rf $(HANDLER) $(PACKAGE).zip

.PHONY: all build pack clean
