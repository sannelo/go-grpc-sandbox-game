.PHONY: all build run clean proto server client noise-test run-server-with-pprof

BINARY_SERVER=game-server
BINARY_CLIENT=game-client
BINARY_NOISETEST=noise-test

all: proto build

build: server client noise-test

server:
	go build -o bin/$(BINARY_SERVER) cmd/server/main.go

client:
	go build -o bin/$(BINARY_CLIENT) cmd/client/main.go

noise-test:
	go build -o bin/$(BINARY_NOISETEST) cmd/noisetest/main.go

run-server:
	go run cmd/server/main.go

run-client:
	go run cmd/client/main.go

run-noise-test:
	go run cmd/noisetest/main.go

run-server-with-pprof:
	go tool pprof http://localhost:6060/debug/pprof/profile

clean:
	go clean
	rm -rf bin/

proto:
	PATH=$$PATH:$$HOME/go/bin protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/game/game.proto
	mkdir -p pkg/protocol/game
	mv proto/game/game.pb.go proto/game/game_grpc.pb.go pkg/protocol/game/ || true

deps:
	go get google.golang.org/grpc
	go get google.golang.org/protobuf/cmd/protoc-gen-go
	go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
	go get github.com/google/uuid
	go get github.com/aquilax/go-perlin

test:
	go test -v ./...

# ... rest of the file remains unchanged ... 