BINARY := bitcoin-retry-endpoint

.PHONY: all build test lint vet clean

all: build

build:
	go build -buildvcs=false -o $(BINARY) .

test:
	go test -race ./...

vet:
	go vet ./...

lint: vet
	golangci-lint run

clean:
	rm -f $(BINARY)
