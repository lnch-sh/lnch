.PHONY: build test install

build:
	go build -o bin/lnch ./cmd/lnch

test:
	go test ./...

install:
	go install ./cmd/lnch
