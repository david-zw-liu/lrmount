BINARY := lrmount
PKG := ./cmd/lrmount

.PHONY: build test vet fmt clean

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
