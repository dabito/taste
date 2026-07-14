BIN := taste
LOCAL_BIN ?= $(HOME)/go/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: install build fmt test vet check clean

install:
	go install -ldflags "$(LDFLAGS)" .
	@echo "Installed $(BIN) $(VERSION) -> $(LOCAL_BIN)/$(BIN)"

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .
	@echo "Built $(BIN) $(VERSION)"

fmt:
	gofmt -w *.go

test:
	go test ./...

vet:
	go vet ./...

check: fmt test vet

clean:
	rm -f $(BIN)
