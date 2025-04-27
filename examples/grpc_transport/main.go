package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ruudniew/mcp-go/client"
	transport "github.com/ruudniew/mcp-go/client/transport"
	"github.com/ruudniew/mcp-go/mcp"
	"github.com/ruudniew/mcp-go/server"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create server
	mcpServer := server.NewMCPServer(
		"example-server",
		"1.0.0",
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithLogging(),
	)

	// Add a simple tool
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Echo back the input"),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("Message to echo"),
		),
	)
	
	mcpServer.AddTool(
		echoTool,
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			message := request.Params.Arguments["message"].(string)
			return mcp.NewToolResultText(fmt.Sprintf("Echo: %s", message)), nil
		},
	)

	// Create gRPC server
	grpcServer := server.NewGRPCServer(mcpServer)

	// Start server in a goroutine
	go func() {
		fmt.Println("Starting gRPC server on :50051...")
		if err := grpcServer.Start(":50051"); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait a moment for the server to start
	time.Sleep(time.Second)

	// Create gRPC transport
	grpcTransport, err := transport.NewGRPC("localhost:50051", transport.WithInsecure())
	if err != nil {
		log.Fatalf("Failed to create gRPC transport: %v", err)
	}
	
	// Create client with the transport
	mcpClient := client.NewClient(grpcTransport)

	// Start the client
	err = mcpClient.Start(ctx)
	if err != nil {
		log.Fatalf("Failed to start client: %v", err)
	}
	defer mcpClient.Close()

	// Initialize the client
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "example-client",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}
	
	_, err = mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize client: %v", err)
	}

	// List available tools
	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}
	fmt.Println("Available tools:")
	for _, tool := range toolsResult.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}

	// Call the echo tool
	callToolRequest := mcp.CallToolRequest{}
	callToolRequest.Params.Name = "echo"
	callToolRequest.Params.Arguments = map[string]interface{}{
		"message": "Hello from gRPC client!",
	}
	
	result, err := mcpClient.CallTool(ctx, callToolRequest)
	if err != nil {
		log.Fatalf("Failed to call tool: %v", err)
	}
	fmt.Printf("Tool result: %v\n", result.Content)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("Server running. Press Ctrl+C to stop.")
	<-sigChan

	fmt.Println("Shutting down...")
	// Shutdown server
	grpcServer.Shutdown(ctx)
	fmt.Println("Server stopped.")
}
