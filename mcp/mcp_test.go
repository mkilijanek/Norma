package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestInProcessServerNamingAndCall(t *testing.T) {
	tools := InProcessServer("calc", []InProcessTool{{
		Name:        "add",
		Description: "add",
		Schema:      map[string]any{"type": "object"},
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct{ A, B float64 }
			json.Unmarshal(args, &a)
			return "sum", nil
		},
	}})
	if len(tools) != 1 || tools[0].Name() != "mcp__calc__add" {
		t.Fatalf("naming wrong: %v", tools[0].Name())
	}
	res, _ := tools[0].Call(context.Background(), []byte(`{"a":1,"b":2}`), nil)
	if res.Flatten() != "sum" {
		t.Fatalf("call result: %q", res.Flatten())
	}
}

// fakeServer speaks just enough MCP JSON-RPC to satisfy initialize, tools/list,
// and tools/call, over the provided pipes.
func fakeServer(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req map[string]any
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		id, hasID := req["id"]
		method, _ := req["method"].(string)
		if !hasID {
			continue // notification
		}
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "echoes", "inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "echoed!"}}}
		}
		enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
}

func TestStdioClientOverPipes(t *testing.T) {
	cReader, sWriter := io.Pipe() // server -> client
	sReader, cWriter := io.Pipe() // client -> server
	go fakeServer(sReader, sWriter)

	ctx := context.Background()
	client, err := newClientWithConn(ctx, "srv", cReader, cWriter, nil)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "mcp__srv__echo" {
		t.Fatalf("tool naming: %+v", tools)
	}
	res, err := tools[0].Call(ctx, []byte(`{"msg":"hi"}`), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(res.Flatten(), "echoed!") {
		t.Fatalf("call result: %q", res.Flatten())
	}
}

func TestRPCErrorMessage(t *testing.T) {
	e := &rpcError{Code: -32601, Message: "method not found"}
	if got, want := e.Error(), "mcp rpc error -32601: method not found"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestClientCloseWithoutTransportIsNoOp(t *testing.T) {
	if err := (&Client{}).Close(); err != nil {
		t.Fatalf("Close on a client with no closer = %v, want nil", err)
	}
}

// TestMCPHelperProcess is not a test: NewStdioClient tests re-exec the test
// binary with GO_WANT_MCP_HELPER=1 so it acts as a minimal MCP server.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	// Refuse to start unless the env passed to NewStdioClient arrived, so a
	// missing variable shows up as a failed handshake.
	if want := os.Getenv("MCP_HELPER_REQUIRE"); want != "" && os.Getenv("MCP_HELPER_TOKEN") != want {
		os.Exit(3)
	}
	fakeServer(os.Stdin, os.Stdout)
	os.Exit(0)
}

func helperCommand(t *testing.T) (string, []string) {
	t.Helper()
	return os.Args[0], []string{"-test.run=^TestMCPHelperProcess$"}
}

func TestNewStdioClientHandshakeToolsAndClose(t *testing.T) {
	t.Setenv("GO_WANT_MCP_HELPER", "1")
	cmd, args := helperCommand(t)
	ctx := context.Background()

	client, err := NewStdioClient(ctx, "proc", cmd, nil, args...)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	tools, err := client.Tools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name() != "mcp__proc__echo" {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}

	sc, ok := client.closer.(*stdioCloser)
	if !ok || sc.cmd == nil || sc.cmd.Process == nil {
		t.Fatalf("closer = %#v, want a *stdioCloser holding a running process", client.closer)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if sc.cmd.ProcessState == nil {
		t.Fatal("Close returned but the child process was not reaped")
	}
}

func TestNewStdioClientPassesEnvToChild(t *testing.T) {
	t.Setenv("GO_WANT_MCP_HELPER", "1")
	t.Setenv("MCP_HELPER_REQUIRE", "s3cret")
	cmd, args := helperCommand(t)
	ctx := context.Background()

	if c, err := NewStdioClient(ctx, "proc", cmd, map[string]string{"MCP_HELPER_TOKEN": "s3cret"}, args...); err != nil {
		t.Fatalf("with the env var the handshake must succeed: %v", err)
	} else {
		c.Close()
	}
	if c, err := NewStdioClient(ctx, "proc", cmd, nil, args...); err == nil {
		c.Close()
		t.Fatal("without the env var the helper exits and the handshake must fail")
	}
}

func TestNewStdioClientMissingBinary(t *testing.T) {
	if _, err := NewStdioClient(context.Background(), "x", "/nonexistent/definitely-not-a-binary", nil); err == nil {
		t.Fatal("want an error when the server binary does not exist")
	}
}
