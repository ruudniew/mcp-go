package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/ruudniew/mcp-go/mcp"
	mcpgrpc "github.com/ruudniew/mcp-go/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	headerKeySessionID = "Mcp-Session-Id"
)

// grpcSession represents an active gRPC connection.
type grpcSession struct {
	sessionID           string
	notificationChannel chan mcp.JSONRPCNotification
	initialized         atomic.Bool
}

func (s *grpcSession) SessionID() string {
	return s.sessionID
}

func (s *grpcSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return s.notificationChannel
}

func (s *grpcSession) Initialize() {
	s.initialized.Store(true)
}

func (s *grpcSession) Initialized() bool {
	return s.initialized.Load()
}

var _ ClientSession = (*grpcSession)(nil)

// GRPCContextFunc is a function that takes an existing context and gRPC metadata
// and returns a potentially modified context.
type GRPCContextFunc func(ctx context.Context, md metadata.MD) context.Context

// GRPCServer implements a gRPC based MCP server.
type GRPCServer struct {
	mcpgrpc.UnimplementedMCPServiceServer
	server      *MCPServer
	sessions    sync.Map
	grpcServer  *grpc.Server
	contextFunc GRPCContextFunc
}

// GRPCOption defines a function type for configuring GRPCServer
type GRPCOption func(*GRPCServer)

// WithGRPCServer sets a custom gRPC server instance
func WithGRPCServer(srv *grpc.Server) GRPCOption {
	return func(s *GRPCServer) {
		s.grpcServer = srv
	}
}

// WithGRPCContextFunc sets a function that will be called to customize the context
// from gRPC metadata.
func WithGRPCContextFunc(fn GRPCContextFunc) GRPCOption {
	return func(s *GRPCServer) {
		s.contextFunc = fn
	}
}

// NewGRPCServer creates a new gRPC server instance with the given MCP server and options.
func NewGRPCServer(server *MCPServer, opts ...GRPCOption) *GRPCServer {
	s := &GRPCServer{
		server: server,
	}

	// Apply all options
	for _, opt := range opts {
		opt(s)
	}

	// Create gRPC server if not provided
	if s.grpcServer == nil {
		s.grpcServer = grpc.NewServer()
	}

	// Register the service
	mcpgrpc.RegisterMCPServiceServer(s.grpcServer, s)

	return s
}

// Start begins serving gRPC connections on the specified address.
func (s *GRPCServer) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	return s.grpcServer.Serve(lis)
}

// Shutdown gracefully stops the gRPC server, closing all active sessions.
func (s *GRPCServer) Shutdown(ctx context.Context) error {
	// Close all sessions
	s.sessions.Range(func(key, value interface{}) bool {
		if session, ok := value.(*grpcSession); ok {
			close(session.notificationChannel)
			s.server.UnregisterSession(ctx, session.sessionID)
		}
		s.sessions.Delete(key)
		return true
	})

	// Gracefully stop the gRPC server
	s.grpcServer.GracefulStop()
	return nil
}

// StreamMessages implements the StreamMessages gRPC method.
// It establishes a bidirectional stream for JSON-RPC messages between client and server.
func (s *GRPCServer) StreamMessages(stream mcpgrpc.MCPService_StreamMessagesServer) error {
	// Extract metadata
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		md = metadata.New(nil)
	}

	// Create context
	ctx := stream.Context()
	if s.contextFunc != nil {
		ctx = s.contextFunc(ctx, md)
	}

	// Get or create session ID
	sessionID := getSessionIDFromMetadata(md)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Create and register session
	session := &grpcSession{
		sessionID:           sessionID,
		notificationChannel: make(chan mcp.JSONRPCNotification, 100),
	}

	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)

	if err := s.server.RegisterSession(ctx, session); err != nil {
		return status.Errorf(codes.Internal, "session registration failed: %v", err)
	}
	defer s.server.UnregisterSession(ctx, sessionID)

	// Create a context with the session
	ctx = s.server.WithContext(ctx, session)

	// Start notification handler for this session
	errChan := make(chan error, 2)
	done := make(chan struct{})
	defer close(done)

	// Goroutine to send notifications back to the client
	go func() {
		for {
			select {
			case notification, ok := <-session.notificationChannel:
				if !ok {
					errChan <- status.Error(codes.Canceled, "notification channel closed")
					return
				}

				// Marshal notification
				notifBytes, err := json.Marshal(notification)
				if err != nil {
					errChan <- status.Errorf(codes.Internal, "failed to marshal notification: %v", err)
					return
				}

				// Send as gRPC message
				err = stream.Send(&mcpgrpc.MCPMessage{
					Content:   notifBytes,
					SessionId: sessionID,
				})
				if err != nil {
					errChan <- status.Errorf(codes.Internal, "failed to send notification: %v", err)
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Goroutine to receive client messages
	go func() {
		for {
			// Receive message from client
			msg, err := stream.Recv()
			if err != nil {
				errChan <- err
				return
			}

			// Process message
			var response mcp.JSONRPCMessage
			response = s.processMessage(ctx, msg.Content)

			// If there's a response, send it back
			if response != nil {
				responseBytes, err := json.Marshal(response)
				if err != nil {
					errChan <- status.Errorf(codes.Internal, "failed to marshal response: %v", err)
					return
				}

				err = stream.Send(&mcpgrpc.MCPMessage{
					Content:   responseBytes,
					SessionId: sessionID,
				})
				if err != nil {
					errChan <- status.Errorf(codes.Internal, "failed to send response: %v", err)
					return
				}
			}
		}
	}()

	// Wait for completion or error
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SendMessage implements the SendMessage gRPC method.
// It handles a single request-response interaction.
func (s *GRPCServer) SendMessage(ctx context.Context, msg *mcpgrpc.MCPMessage) (*mcpgrpc.MCPMessage, error) {
	// Extract metadata
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	}

	// Apply context function if provided
	if s.contextFunc != nil {
		ctx = s.contextFunc(ctx, md)
	}

	// Get session ID from message or metadata
	sessionID := msg.SessionId
	if sessionID == "" {
		sessionID = getSessionIDFromMetadata(md)
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Check if we have an existing session
	var session *grpcSession
	sessionObj, found := s.sessions.Load(sessionID)
	if found {
		session = sessionObj.(*grpcSession)
	} else {
		// Create a new session for this request
		session = &grpcSession{
			sessionID:           sessionID,
			notificationChannel: make(chan mcp.JSONRPCNotification, 100),
		}
		s.sessions.Store(sessionID, session)

		if err := s.server.RegisterSession(ctx, session); err != nil {
			s.sessions.Delete(sessionID)
			return nil, status.Errorf(codes.Internal, "session registration failed: %v", err)
		}
	}

	// Set the session in the context
	ctx = s.server.WithContext(ctx, session)

	// Process the message
	response := s.processMessage(ctx, msg.Content)
	if response == nil {
		// No response for notification
		return &mcpgrpc.MCPMessage{
			SessionId: sessionID,
		}, nil
	}

	// Marshal response
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &mcpgrpc.MCPMessage{
		Content:   responseBytes,
		SessionId: sessionID,
	}, nil
}

// processMessage handles the raw JSON message and returns a response if needed.
func (s *GRPCServer) processMessage(ctx context.Context, msgBytes []byte) mcp.JSONRPCMessage {
	return s.server.HandleMessage(ctx, msgBytes)
}

// getSessionIDFromMetadata extracts the session ID from gRPC metadata.
func getSessionIDFromMetadata(md metadata.MD) string {
	values := md.Get(headerKeySessionID)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
