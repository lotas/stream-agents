BIN := stream-agents

.PHONY: build run tray test clean

build:
	go build -o $(BIN) ./cmd/server

run: build
	./$(BIN)

tray: build
	./$(BIN) -tray

test:
	go test ./...

clean:
	rm -f $(BIN)
