all:
	go build ./...

test:
	go test ./...

# Optional: generate gRPC stubs (requires protoc + protoc-gen-go + protoc-gen-go-grpc)
proto:
	protoc -I ./pkg/protocol/proto \
		--go_out=paths=source_relative:./pkg/protocol/proto \
		--go-grpc_out=paths=source_relative:./pkg/protocol/proto \
		./pkg/protocol/proto/common.proto \
		./pkg/protocol/proto/routing.proto \
		./pkg/protocol/proto/task.proto \
		./pkg/protocol/proto/control.proto \
		./pkg/protocol/proto/tiny.proto \
		./pkg/protocol/proto/ttmesh.proto
