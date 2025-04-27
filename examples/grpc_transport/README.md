# MCP gRPC Transport Example

This example demonstrates how to use the gRPC transport with Model Context Protocol (MCP).

## Overview

The gRPC transport provides a high-performance, bidirectional streaming communication channel between MCP clients and servers. It offers several advantages over other transport methods:

- Bidirectional streaming for real-time communication
- Efficient binary serialization with Protocol Buffers
- Strong typing with protocol definitions
- Support for multiple programming languages
- Built-in connection management and error handling

## Prerequisites

Before running this example:

1. Make sure you have the Protocol Buffers compiler (protoc) installed:
   ```
   # On macOS
   brew install protobuf

   # On Ubuntu/Debian
   apt-get install -y protobuf-compiler
   ```

2. Generate the Protocol Buffer code:
   ```
   task proto
   ```

## Running the Example

To run this example:

1. Make sure you have the required dependencies:
   ```
   task tidy
   ```

2. Run the example:
   ```
   task example-grpc
   ```

3. The example will:
   - Start a gRPC server on port 50051
   - Connect to it with a client
   - Initialize the MCP session
   - List available tools
   - Call the "echo" tool with a test message
   - Wait for Ctrl+C to shut down gracefully

## Using gRPC Transport in Your Own Applications

### Server Side

```go
// Create MCP server
mcpServer := server.NewMCPServer(
    "my-server",
    "1.0.0",
    server.WithResourceCapabilities(true, true),
    server.WithToolCapabilities(true),
)

// Add tools, resources, prompts as needed
mcpServer.AddTool(...)

// Create gRPC server
grpcServer := server.NewGRPCServer(mcpServer)

// Start server
if err := grpcServer.Start(":50051"); err != nil {
    log.Fatalf("Failed to start server: %v", err)
}
```

### Client Side

```go
// Create MCP client
mcpClient, err := client.NewMCPClient("my-client", "1.0.0")
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}

// Create gRPC transport
grpcTransport, err := transport.NewGRPC("localhost:50051", transport.WithInsecure())
if err != nil {
    log.Fatalf("Failed to create gRPC transport: %v", err)
}

// Use the transport
ctx := context.Background()
err = mcpClient.Start(ctx, grpcTransport)
if err != nil {
    log.Fatalf("Failed to start client: %v", err)
}
defer mcpClient.Close()

// Initialize the client
err = mcpClient.Initialize(ctx)
if err != nil {
    log.Fatalf("Failed to initialize client: %v", err)
}

// Use MCP functionality
tools, err := mcpClient.ListTools(ctx)
// ...
```

## Security Considerations

In production environments:

1. Always use secure gRPC connections with TLS
2. Implement proper authentication
3. Set appropriate timeout and retry policies
4. Monitor connection health

The example uses `WithInsecure()` for simplicity, but this should be replaced with proper TLS in production.
