BIN := taste
LOCAL_BIN ?= $(HOME)/go/bin

.PHONY: install fmt test vet check clean

install:
	go install .
	@echo "Installed $(BIN) -> $(LOCAL_BIN)/$(BIN)"

fmt:
	gofmt -w *.go

test:
	go test ./...

vet:
	go vet ./...

check: fmt test vet

clean:
	rm -f $(BIN)
