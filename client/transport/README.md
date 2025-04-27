# gRPC Transport for Model Context Protocol

This package provides a gRPC-based transport implementation for the Model Context Protocol (MCP). 
It enables high-performance, bidirectional streaming communication between MCP clients and servers.

## Features

- Bidirectional streaming for real-time communication
- Session management across connections
- Support for both streaming and single request/response patterns
- Efficient binary serialization with Protocol Buffers

## Usage

### Client Side

```go
// Create a gRPC transport
grpcTransport, err := transport.NewGRPC(
    "localhost:50051",              // Server address
    transport.WithInsecure(),       // Use insecure connection (for development only)
    // Additional options as needed
)
if err != nil {
    log.Fatalf("Failed to create transport: %v", err)
}

// Create MCP client
mcpClient, err := client.NewMCPClient("my-client", "1.0.0")
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}

// Start client with the transport
ctx := context.Background()
if err := mcpClient.Start(ctx, grpcTransport); err != nil {
    log.Fatalf("Failed to start client: %v", err)
}
defer mcpClient.Close()

// Initialize the session
if err := mcpClient.Initialize(ctx); err != nil {
    log.Fatalf("Failed to initialize client: %v", err)
}

// Use MCP functionality as usual
tools, err := mcpClient.ListTools(ctx)
// ...
```

### Server Side

```go
// Create MCP server
mcpServer := server.NewMCPServer(
    "my-server",
    "1.0.0",
    server.WithResourceCapabilities(true, true),
    server.WithToolCapabilities(true),
    // Additional options as needed
)

// Register tools, resources, etc.
mcpServer.AddTool(...)

// Create gRPC server
grpcServer := server.NewGRPCServer(mcpServer)

// Start the server
if err := grpcServer.Start(":50051"); err != nil {
    log.Fatalf("Failed to start server: %v", err)
}
```

## Security Considerations

For production use:

1. **Enable TLS**: Avoid using `WithInsecure()` in production environments
   ```go
   // Load TLS credentials
   creds, err := credentials.NewServerTLSFromFile("server.crt", "server.key")
   if err != nil {
       log.Fatalf("Failed to load TLS credentials: %v", err)
   }
   
   // Create secure gRPC server
   grpcServer := grpc.NewServer(grpc.Creds(creds))
   mcpGrpcServer := server.NewGRPCServer(mcpServer, server.WithGRPCServer(grpcServer))
   ```

2. **Implement Authentication**: Use interceptors for authentication
   ```go
   // Create authentication interceptor
   authInterceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
       // Authentication logic
       return handler(ctx, req)
   }
   
   // Apply to server
   grpcServer := grpc.NewServer(
       grpc.UnaryInterceptor(authInterceptor),
   )
   ```

3. **Set Request Limits**: Prevent resource exhaustion
   ```go
   grpcServer := grpc.NewServer(
       grpc.MaxRecvMsgSize(4 * 1024 * 1024), // 4MB
       grpc.MaxSendMsgSize(4 * 1024 * 1024), // 4MB
   )
   ```

## Performance Tuning

For high-performance scenarios:

1. **Connection Pooling**: Use multiple connections in high-load scenarios
2. **Compression**: Enable gRPC compression for large messages
   ```go
   grpcServer := grpc.NewServer(
       grpc.RPCCompressor(grpc.NewGZIPCompressor()),
   )
   ```
3. **Keepalive**: Configure keepalive to maintain long-lived connections
   ```go
   grpcServer := grpc.NewServer(
       grpc.KeepaliveParams(keepalive.ServerParameters{
           MaxConnectionIdle: 5 * time.Minute,
           Time:              1 * time.Minute,
           Timeout:           10 * time.Second,
       }),
   )
   ```

## Error Handling

The transport provides comprehensive error handling for various failure scenarios:

- Connection failures
- Message encoding/decoding errors
- Server errors
- Timeouts

Clients should implement appropriate retry logic for transient failures.
