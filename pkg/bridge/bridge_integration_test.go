//lint:file-ignore SA1019 grpc.DialContext and WithBlock are retained in tests to validate blocking dial behavior on grpc 1.x.

package bridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// buildBridgeTestTodoService implements the test gRPC backend used by bridge integration tests.
type buildBridgeTestTodoService struct {
	proto.UnimplementedTodoServiceServer
}

// CreateTodo CreateTodo returns a deterministic todo for bridge integration tests.
func (buildBridgeTestTodoService) CreateTodo(parseCtx context.Context, parseReq *proto.CreateTodoRequest) (*proto.CreateTodoResponse, error) {
	return &proto.CreateTodoResponse{
		Todo: &proto.Todo{
			Id:   "bridge-test",
			Text: parseReq.Text,
			Done: false,
		},
	}, nil
}

// buildBridgeTestBackend starts a real gRPC server for bridge end-to-end tests.
func buildBridgeTestBackend(parseT *testing.T) (string, func()) {
	parseT.Helper()

	parseBackendListener, parseErr := net.Listen("tcp", "127.0.0.1:0")
	if parseErr != nil {
		parseT.Fatalf("Listen() error: %v", parseErr)
	}

	parseBackendServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseBackendServer, buildBridgeTestTodoService{})

	go func() {
		_ = parseBackendServer.Serve(parseBackendListener)
	}()

	return parseBackendListener.Addr().String(), func() {
		parseBackendServer.Stop()
		_ = parseBackendListener.Close()
	}
}

// TestHandleBridgeEndToEnd verifies the bridge upgrades, proxies, and tears down cleanly.
func TestHandleBridgeEndToEnd(parseT *testing.T) {
	parseTargetAddress, clearBackend := buildBridgeTestBackend(parseT)
	defer clearBackend()

	parseConnectSignal := make(chan struct{}, 1)
	parseDisconnectSignal := make(chan struct{}, 1)

	parseHandler := NewHandler(Config{
		TargetAddress: parseTargetAddress,
		OnConnect: func(parseR *http.Request) {
			select {
			case parseConnectSignal <- struct{}{}:
			default:
			}
		},
		OnDisconnect: func(parseR *http.Request) {
			select {
			case parseDisconnectSignal <- struct{}{}:
			default:
			}
		},
	})

	parseBridgeServer := httptest.NewServer(parseHandler)
	defer parseBridgeServer.Close()

	parseDialContext, clearDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer clearDial()

	parseBridgeURL := "ws" + strings.TrimPrefix(parseBridgeServer.URL, "http")
	parseClientConn, parseErr := grpc.DialContext(
		parseDialContext,
		"ignored:1234",
		DialOption(parseBridgeURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("DialContext() error: %v", parseErr)
	}

	select {
	case <-parseConnectSignal:
	case <-time.After(2 * time.Second):
		parseT.Fatal("Timed out waiting for OnConnect callback")
	}

	parseTodoClient := proto.NewTodoServiceClient(parseClientConn)
	parseTodoResponse, parseErr := parseTodoClient.CreateTodo(parseDialContext, &proto.CreateTodoRequest{
		Text: "bridge-rpc",
	})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo() error: %v", parseErr)
	}
	if parseTodoResponse.GetTodo().GetText() != "bridge-rpc" {
		parseT.Fatalf("CreateTodo() text = %q, want %q", parseTodoResponse.GetTodo().GetText(), "bridge-rpc")
	}

	if parseErr = parseClientConn.Close(); parseErr != nil {
		parseT.Fatalf("Close() error: %v", parseErr)
	}

	select {
	case <-parseDisconnectSignal:
	case <-time.After(2 * time.Second):
		parseT.Fatal("Timed out waiting for OnDisconnect callback")
	}
}

// TestParseBridgeTargetURL verifies target validation across success and error paths.
func TestParseBridgeTargetURL(parseT *testing.T) {
	parseTests := []struct {
		parseName      string
		parseTarget    string
		isWantError    bool
		parseHostValue string
	}{
		{
			parseName:   "empty target",
			parseTarget: "",
			isWantError: true,
		},
		{
			parseName:   "malformed target",
			parseTarget: "%",
			isWantError: true,
		},
		{
			parseName:   "missing host",
			parseTarget: "/grpc",
			isWantError: true,
		},
		{
			parseName:      "valid host",
			parseTarget:    "127.0.0.1:50051",
			parseHostValue: "127.0.0.1:50051",
		},
		{
			parseName:      "http url target",
			parseTarget:    "http://127.0.0.1:50051",
			parseHostValue: "127.0.0.1:50051",
		},
		{
			parseName:   "https url target unsupported",
			parseTarget: "https://127.0.0.1:50051",
			isWantError: true,
		},
		{
			parseName:   "url target with path unsupported",
			parseTarget: "http://127.0.0.1:50051/grpc",
			isWantError: true,
		},
		{
			parseName:   "host target with path unsupported",
			parseTarget: "127.0.0.1:50051/grpc",
			isWantError: true,
		},
		{
			parseName:   "host target with query unsupported",
			parseTarget: "127.0.0.1:50051?mode=h2c",
			isWantError: true,
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseTargetURL, parseErr := parseBridgeTargetURL(parseTestCase.parseTarget)
			if parseTestCase.isWantError {
				if parseErr == nil {
					parseT2.Fatal("parseBridgeTargetURL() expected error, got nil")
				}
				return
			}

			if parseErr != nil {
				parseT2.Fatalf("parseBridgeTargetURL() error: %v", parseErr)
			}
			if parseTargetURL.Host != parseTestCase.parseHostValue {
				parseT2.Fatalf("parseBridgeTargetURL() host = %q, want %q", parseTargetURL.Host, parseTestCase.parseHostValue)
			}
		})
	}
}

// TestHandleBridgeProxyError verifies the reverse proxy error handler returns a 502 and logs.
func TestHandleBridgeProxyError(parseT *testing.T) {
	parseLogger := &testLogger{}
	parseHandler := NewHandler(Config{
		TargetAddress: "127.0.0.1:50051",
		Logger:        parseLogger,
	})

	parseRecorder := httptest.NewRecorder()
	parseRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	parseProxyError := errors.New("backend unreachable")

	parseHandler.proxy.ErrorHandler(parseRecorder, parseRequest, parseProxyError)

	if parseRecorder.Code != http.StatusBadGateway {
		parseT.Fatalf("ErrorHandler status = %d, want %d", parseRecorder.Code, http.StatusBadGateway)
	}
	if !strings.Contains(parseRecorder.Body.String(), http.StatusText(http.StatusBadGateway)) {
		parseT.Fatalf("ErrorHandler body = %q, want generic bad gateway error", parseRecorder.Body.String())
	}

	parseFoundProxyLog := false
	for _, parseMessage := range parseLogger.messages {
		if strings.Contains(parseMessage, "Proxy error") {
			parseFoundProxyLog = true
			break
		}
	}
	if !parseFoundProxyLog {
		parseT.Fatal("Expected proxy error log entry")
	}
}
