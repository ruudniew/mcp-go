// Package proto contains Protocol Buffer definitions and generated code for MCP gRPC transport.
//
//go:generate protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative mcp.proto
package proto
