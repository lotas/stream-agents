BIN := stream-agents

.PHONY: build run test clean

build:
	go build -o $(BIN) ./cmd/server

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -f $(BIN)
