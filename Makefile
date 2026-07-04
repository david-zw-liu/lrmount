BINARY := lrmount
PKG := ./cmd/lrmount

.DEFAULT_GOAL := run

.PHONY: run build test vet fmt clean

run: build
	./$(BINARY)

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
