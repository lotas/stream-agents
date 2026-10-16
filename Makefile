BIN := stream-agents
LISTEN ?= 0.0.0.0:7777

.PHONY: build run tray test clean

build:
	go build -o $(BIN) ./cmd/server

run: build
	./$(BIN) -listen $(LISTEN)

tray: build
	./$(BIN) -tray -listen $(LISTEN)

test:
	go test ./...

clean:
	rm -f $(BIN)
