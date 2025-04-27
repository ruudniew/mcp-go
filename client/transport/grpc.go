package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ruudniew/mcp-go/mcp"
	mcpgrpc "github.com/ruudniew/mcp-go/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// GRPC implements a gRPC client transport for MCP.
//
// It provides bidirectional streaming communication over gRPC, allowing for
// real-time messaging between client and server.
//
// https://modelcontextprotocol.io/specification/2025-03-26/basic/transports
type GRPC struct {
	target    string
	conn      *grpc.ClientConn
	client    mcpgrpc.MCPServiceClient
	stream    mcpgrpc.MCPService_StreamMessagesClient
	sessionID atomic.Value // string

	notificationHandler func(mcp.JSONRPCNotification)
	notifyMu            sync.RWMutex

	closed         chan struct{}
	streamDone     chan struct{}
	streamCtx      context.Context
	streamCancel   context.CancelFunc
	streamFinished bool

	opts []grpc.DialOption
}

// GRPCOption is a function that configures a GRPC transport.
type GRPCOption func(*GRPC)

// WithGRPCDialOptions adds additional gRPC dial options.
func WithGRPCDialOptions(opts ...grpc.DialOption) GRPCOption {
	return func(g *GRPC) {
		g.opts = append(g.opts, opts...)
	}
}

// WithInsecure configures the gRPC client to use insecure transport.
// This should only be used for testing or in trusted environments.
func WithInsecure() GRPCOption {
	return func(g *GRPC) {
		g.opts = append(g.opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// NewGRPC creates a new GRPC client transport.
func NewGRPC(target string, options ...GRPCOption) (*GRPC, error) {
	g := &GRPC{
		target:     target,
		closed:     make(chan struct{}),
		streamDone: make(chan struct{}),
		opts:       []grpc.DialOption{},
	}
	g.sessionID.Store("") // Initialize empty session ID

	// Apply options
	for _, opt := range options {
		opt(g)
	}

	return g, nil
}

// Start initiates the gRPC connection to the server.
func (g *GRPC) Start(ctx context.Context) error {
	// Check if already closed
	select {
	case <-g.closed:
		return fmt.Errorf("transport already closed")
	default:
	}

	// Connect to the gRPC server
	conn, err := grpc.DialContext(ctx, g.target, g.opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to gRPC server: %w", err)
	}
	g.conn = conn

	// Create client
	g.client = mcpgrpc.NewMCPServiceClient(conn)

	// Start the message streaming
	g.streamCtx, g.streamCancel = context.WithCancel(context.Background())

	// Add session ID to outgoing context if we have one
	var streamCtx context.Context
	sessionID := g.sessionID.Load().(string)
	if sessionID != "" {
		streamCtx = metadata.AppendToOutgoingContext(g.streamCtx, headerKeySessionID, sessionID)
	} else {
		streamCtx = g.streamCtx
	}

	// Establish bidirectional stream
	stream, err := g.client.StreamMessages(streamCtx)
	if err != nil {
		g.streamCancel()
		return fmt.Errorf("failed to create stream: %w", err)
	}
	g.stream = stream

	// Start goroutine to receive messages
	go g.receiveMessages()

	return nil
}

// Close closes the gRPC connection to the server.
func (g *GRPC) Close() error {
	select {
	case <-g.closed:
		return nil // Already closed
	default:
		close(g.closed)
	}

	// Cancel the stream context
	if g.streamCancel != nil {
		g.streamCancel()
	}

	// Wait for the stream to finish
	<-g.streamDone

	// Close the connection
	if g.conn != nil {
		return g.conn.Close()
	}

	return nil
}

// SendRequest sends a JSON-RPC request to the server and waits for a response.
func (g *GRPC) SendRequest(
	ctx context.Context,
	request JSONRPCRequest,
) (*JSONRPCResponse, error) {
	// Create a channel to receive the response
	responseChan := make(chan *JSONRPCResponse, 1)

	// Store request ID for later matching with response
	requestID := request.ID

	// Marshal request
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create gRPC message
	msg := &mcpgrpc.MCPMessage{
		Content: requestBytes,
	}

	// Add session ID if available
	sessionID := g.sessionID.Load().(string)
	if sessionID != "" {
		msg.SessionId = sessionID
	}

	// Send message using the stream
	if g.stream != nil {
		err = g.stream.Send(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to send request via stream: %w", err)
		}

		// TODO: Add a map to track request ID -> response channel for async handling
		// Currently this won't work without additional implementation for matching
		// responses with requests when using streaming
	} else {
		// If we don't have a stream, send as a single message
		resp, err := g.client.SendMessage(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}

		// Parse the response
		var response JSONRPCResponse
		if err := json.Unmarshal(resp.Content, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		// Verify response ID matches request ID
		if requestID != 0 && response.ID != nil && requestID != *response.ID {
			return nil, fmt.Errorf("response ID does not match request ID")
		}

		// Check for session ID in response
		if request.Method == initializeMethod && resp.SessionId != "" {
			g.sessionID.Store(resp.SessionId)
		}

		return &response, nil
	}

	// Wait for response via the stream
	select {
	case response := <-responseChan:
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.closed:
		return nil, fmt.Errorf("transport closed")
	}
}

// SendNotification sends a JSON-RPC notification to the server.
func (g *GRPC) SendNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	// Marshal notification
	notificationBytes, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	// Create gRPC message
	msg := &mcpgrpc.MCPMessage{
		Content: notificationBytes,
	}

	// Add session ID if available
	sessionID := g.sessionID.Load().(string)
	if sessionID != "" {
		msg.SessionId = sessionID
	}

	// Send message
	if g.stream != nil {
		err = g.stream.Send(msg)
		if err != nil {
			return fmt.Errorf("failed to send notification via stream: %w", err)
		}
	} else {
		// If we don't have a stream, send as a single message
		_, err := g.client.SendMessage(ctx, msg)
		if err != nil {
			return fmt.Errorf("failed to send notification: %w", err)
		}
	}

	return nil
}

// receiveMessages continuously receives messages from the stream.
func (g *GRPC) receiveMessages() {
	defer close(g.streamDone)

	for {
		select {
		case <-g.closed:
			return
		case <-g.streamCtx.Done():
			return
		default:
			// Receive message from the stream
			msg, err := g.stream.Recv()
			if err != nil {
				if err == io.EOF || g.streamCtx.Err() != nil {
					// Stream ended normally or context canceled
					return
				}
				// Log the error but don't exit the loop
				fmt.Printf("Error receiving message: %v\n", err)
				time.Sleep(100 * time.Millisecond) // Avoid tight loop on error
				continue
			}

			// First check message type by decoding as a generic JSON object
			var jsonrpcMsg map[string]interface{}
			if err := json.Unmarshal(msg.Content, &jsonrpcMsg); err != nil {
				fmt.Printf("Failed to unmarshal message: %v\n", err)
				continue
			}

			// Check if it's a notification (no ID field)
			if _, hasID := jsonrpcMsg["id"]; !hasID {
				var notification mcp.JSONRPCNotification
				if err := json.Unmarshal(msg.Content, &notification); err != nil {
					fmt.Printf("Failed to unmarshal notification: %v\n", err)
					continue
				}

				// Handle the notification
				g.notifyMu.RLock()
				if g.notificationHandler != nil {
					g.notificationHandler(notification)
				}
				g.notifyMu.RUnlock()
			} else {
				// It's a response
				var response JSONRPCResponse
				if err := json.Unmarshal(msg.Content, &response); err != nil {
					fmt.Printf("Failed to unmarshal response: %v\n", err)
					continue
				}

				// Save session ID if it's initialize response
				if msg.SessionId != "" {
					g.sessionID.Store(msg.SessionId)
				}

				// TODO: Once we have request tracking, we should dispatch this
				// response to the appropriate waiting goroutine
			}
		}
	}
}

// SetNotificationHandler sets the handler for notifications from the server.
func (g *GRPC) SetNotificationHandler(handler func(mcp.JSONRPCNotification)) {
	g.notifyMu.Lock()
	g.notificationHandler = handler
	g.notifyMu.Unlock()
}

// GetSessionId returns the current session ID.
func (g *GRPC) GetSessionId() string {
	return g.sessionID.Load().(string)
}
