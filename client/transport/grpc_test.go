package transport_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ruudniew/mcp-go/client"
	"github.com/ruudniew/mcp-go/client/transport"
	"github.com/ruudniew/mcp-go/mcp"
	"github.com/ruudniew/mcp-go/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCTransport(t *testing.T) {
	// Skip test if protobuf hasn't been generated yet
	t.Skip("Skipping gRPC test until protobuf files are generated")

	// Create a buffer for grpc connections
	lis := bufconn.Listen(1024 * 1024)
	
	// Create MCP server
	mcpServer := server.NewMCPServer(
		"test-server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)
	
	// Add a simple echo tool
	mcpServer.AddTool(
		mcp.Tool{
			Name:        "echo",
			Description: "Echo back the input",
			Parameters: []mcp.Parameter{
				{
					Name:        "message",
					Description: "Message to echo",
					Type:        mcp.PARAM_TYPE_STRING,
					Required:    true,
				},
			},
		},
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			message := request.Params.Arguments["message"].(string)
			return &mcp.CallToolResult{
				Content: "Echo: " + message,
			}, nil
		},
	)
	
	// Create gRPC server with custom gRPC server
	grpcServer := grpc.NewServer()
	grpcHandler := server.NewGRPCServer(mcpServer, server.WithGRPCServer(grpcServer))
	
	// Register and start server
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Errorf("Failed to serve: %v", err)
		}
	}()
	defer grpcServer.GracefulStop()
	
	// Create a custom dialer for the bufconn
	bufDialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	
	// Create client with custom dialer
	ctx := context.Background()
	
	// Create MCP client
	mcpClient, err := client.NewMCPClient("test-client", "1.0.0")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	
	// Create gRPC transport with custom dialer
	grpcTransport, err := transport.NewGRPC("bufnet", 
		transport.WithGRPCDialOptions(
			grpc.WithContextDialer(bufDialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		),
	)
	if err != nil {
		t.Fatalf("Failed to create gRPC transport: %v", err)
	}
	
	// Start client
	err = mcpClient.Start(ctx, grpcTransport)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer mcpClient.Close()
	
	// Initialize client
	err = mcpClient.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}
	
	// Test the echo tool
	toolCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	
	result, err := mcpClient.CallTool(toolCtx, "echo", map[string]interface{}{
		"message": "Hello gRPC!",
	})
	
	if err != nil {
		t.Fatalf("Failed to call tool: %v", err)
	}
	
	if result.Content != "Echo: Hello gRPC!" {
		t.Errorf("Expected 'Echo: Hello gRPC!', got %q", result.Content)
	}
}
