package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"
	"github.com/azure/azure-functions-golang-worker/tests/integration/internal/testhost"
)

func TestGenericTriggerQueues(t *testing.T) {
	requireAzurite(t)
	service, err := azqueue.NewServiceClientFromConnectionString(azuriteConnStr, nil)
	if err != nil {
		t.Fatalf("create queue service client: %v", err)
	}
	unique := strings.ToLower(rand.Text()[:20])
	cases := []struct {
		function string
		setting  string
		suffix   string
		body     string
		log      string
	}{
		{"RawQueue", "GenericRawQueue", "raw", "raw-" + unique + ` {"literal":true}`, ""},
		{"JSONQueue", "GenericJSONQueue", "json", fmt.Sprintf(`{"id":"json-%s","quantity":7}`, unique), "typed order trigger_type=queueTrigger id=json-" + unique + " quantity=7"},
		{"TypedQueue", "GenericTypedQueue", "typed", fmt.Sprintf(`{"id":"typed-%s","quantity":11}`, unique), "typed order trigger_type=queueTrigger id=typed-" + unique + " quantity=11"},
		{"MetadataQueue", "GenericMetadataQueue", "metadata", "metadata-" + unique, "order metadata trigger_type=queueTrigger body=metadata-" + unique},
	}
	cases[0].log = "raw order trigger_type=queueTrigger body=" + strconv.Quote(cases[0].body)
	env := make(map[string]string, len(cases))
	for _, tc := range cases {
		queueName := "generic-" + unique + "-" + tc.suffix
		env[tc.setting] = queueName
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err := service.CreateQueue(ctx, queueName, nil)
		cancel()
		if err != nil {
			t.Fatalf("create queue %s: %v", queueName, err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := service.DeleteQueue(ctx, queueName, nil); err != nil {
				t.Errorf("delete test queue %s: %v", queueName, err)
			}
		})
	}
	// Register host cleanup after queue cleanup so the host stops before deletion.
	host := startGenericTriggerHost(t, "queues", env)
	for _, tc := range cases {
		t.Run(tc.function, func(t *testing.T) {
			queue := service.NewQueueClient(env[tc.setting])
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			message, err := queue.EnqueueMessage(ctx, base64.StdEncoding.EncodeToString([]byte(tc.body)), nil)
			if err != nil {
				t.Fatalf("enqueue %s message: %v", tc.function, err)
			}
			assertHostLogContains(t, host, tc.log, 30*time.Second)
			assertHostLogContains(t, host, "generic middleware trigger_type=queueTrigger function="+tc.function+" trigger=queueTrigger", 5*time.Second)
			assertHostLogContains(t, host, "Executed 'Functions."+tc.function+"' (Succeeded", 10*time.Second)
			if tc.function == "MetadataQueue" {
				if len(message.Messages) != 1 || message.Messages[0] == nil || message.Messages[0].MessageID == nil {
					t.Fatal("enqueued metadata message has no ID")
				}
				assertGenericQueueMetadata(t, host, tc.log, *message.Messages[0].MessageID)
			}
		})
	}
}

func assertGenericQueueMetadata(t *testing.T, host testhost.Host, marker, messageID string) {
	t.Helper()
	data, err := os.ReadFile(host.LogPath())
	if err != nil {
		t.Fatalf("read metadata log: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, marker+" metadata=") {
			continue
		}
		_, encoded, _ := strings.Cut(line, " metadata=")
		value, err := strconv.Unquote(strings.TrimSpace(encoded))
		if err != nil {
			t.Fatalf("unquote metadata JSON: %v", err)
		}
		var metadata struct {
			ID              string `json:"Id"`
			DequeueCount    int64
			InsertionTime   string
			ExpirationTime  string
			NextVisibleTime string
			PopReceipt      string
		}
		if err := json.Unmarshal([]byte(value), &metadata); err != nil {
			t.Fatalf("decode metadata JSON: %v", err)
		}
		if metadata.ID != messageID || metadata.DequeueCount != 1 {
			t.Errorf("metadata Id=%q DequeueCount=%d, want Id=%q DequeueCount=1", metadata.ID, metadata.DequeueCount, messageID)
		}
		if metadata.PopReceipt == "" {
			t.Error("metadata PopReceipt is empty")
		}
		for name, value := range map[string]string{
			"InsertionTime": metadata.InsertionTime, "ExpirationTime": metadata.ExpirationTime, "NextVisibleTime": metadata.NextVisibleTime,
		} {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				t.Errorf("metadata %s is not an RFC3339 timestamp: %v", name, err)
			}
		}
		return
	}
	t.Fatal("metadata JSON log for the unique message is missing")
}

func TestGenericTriggerMCP(t *testing.T) {
	requireAzurite(t)
	host := startGenericTriggerHost(t, "mcp", nil)
	client := mcpTestClient{
		endpoint: host.URL() + "/runtime/webhooks/mcp",
		client:   &http.Client{Timeout: 30 * time.Second},
	}
	t.Cleanup(client.client.CloseIdleConnections)
	initialized := client.request(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"clientInfo":      map[string]string{"name": "go-worker-test", "version": "1.0"},
		"capabilities":    map[string]any{},
	})
	var initialization struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initialized, &initialization); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initialization.ProtocolVersion != "2025-03-26" {
		t.Fatalf("MCP protocol version = %q, want 2025-03-26", initialization.ProtocolVersion)
	}
	client.request(t, 0, "notifications/initialized", map[string]any{})
	listed := client.request(t, 2, "tools/list", map[string]any{})
	var listing struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type       string `json:"type"`
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listed, &listing); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	echoCount := 0
	for _, tool := range listing.Tools {
		if tool.Name != "echo" {
			continue
		}
		echoCount++
		if tool.InputSchema.Type != "object" || tool.InputSchema.Properties["text"].Type != "string" || !slices.Contains(tool.InputSchema.Required, "text") {
			t.Fatalf("echo schema must describe an object with required string text: %+v", tool.InputSchema)
		}
	}
	if echoCount != 1 {
		t.Fatalf("tools/list contains %d echo tools, want 1", echoCount)
	}
	text := "generic-echo-" + strings.ToLower(rand.Text())
	called := client.request(t, 3, "tools/call", map[string]any{
		"name": "echo", "arguments": map[string]string{"text": text},
	})
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(called, &result); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != text {
		t.Fatalf("tools/call result = %+v, want echoed text %q", result, text)
	}
	assertHostLogContains(t, host, "echo tool trigger_type=mcpToolTrigger text="+text, 5*time.Second)
	assertHostLogContains(t, host, "generic middleware trigger_type=mcpToolTrigger function=EchoTool trigger=mcpToolTrigger", 5*time.Second)
	assertHostLogContains(t, host, "Executed 'Functions.EchoTool' (Succeeded", 10*time.Second)
}

func startGenericTriggerHost(t *testing.T, scenario string, settings map[string]string) testhost.Host {
	t.Helper()
	appDir := t.TempDir()
	binDir := filepath.Join(appDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create sample binary directory: %v", err)
	}
	appName := "app"
	if runtime.GOOS == "windows" {
		appName += ".exe"
	}
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", filepath.Join(binDir, appName), "./samples/genericTriggers")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build genericTriggers sample: %v\n%s", err, output)
	}
	hostJSON, err := os.ReadFile(filepath.Join(samplesDir(), "genericTriggers", "host.json"))
	if err != nil {
		t.Fatalf("read genericTriggers host configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "host.json"), hostJSON, 0o600); err != nil {
		t.Fatalf("copy genericTriggers host configuration: %v", err)
	}
	env := make(map[string]string, len(settings)+4)
	for key, value := range settings {
		env[key] = value
	}
	hostID := "generic-" + strings.ToLower(rand.Text()[:20])
	env["GOWORK"] = "off"
	env["AzureWebJobsStorage"] = azuriteConnStr
	env["AzureFunctionsWebHost__hostid"] = hostID
	env["GENERIC_SCENARIO"] = scenario
	host, err := testhost.Start(context.Background(), testhost.Config{
		SampleDir: appDir, FuncExe: funcExe(), NoBuild: true, Environment: env,
		ArtifactDir: filepath.Join("artifacts", t.Name(), hostID), InitTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("start genericTriggers %s host: %v", scenario, err)
	}
	t.Logf("genericTriggers %s host log: %s", scenario, host.LogPath())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Stop(ctx); err != nil {
			t.Errorf("stop genericTriggers host: %v", err)
		}
	})
	return host
}

type mcpTestClient struct {
	endpoint  string
	sessionID string
	client    *http.Client
}

// An id of zero sends a notification, which has no JSON-RPC id or result.
func (c *mcpTestClient) request(t *testing.T, id int, method string, params any) json.RawMessage {
	t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != 0 {
		message["id"] = id
	}
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("encode %s request: %v", method, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatalf("send %s request: %v", method, err)
	}
	defer resp.Body.Close()
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		c.sessionID = sessionID
	}
	if id == 0 {
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s HTTP status = %d, want 202", method, resp.StatusCode)
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s HTTP status = %d, want 200", method, resp.StatusCode)
	}
	result, err := parseMCPResponse(resp.Body, resp.Header.Get("Content-Type"), id)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	return result
}

// parseMCPResponse accepts JSON or SSE, selecting the requested JSON-RPC id.
// SSE is read incrementally so an open stream need not close after its response.
func parseMCPResponse(reader io.Reader, contentType string, id int) (json.RawMessage, error) {
	const maxBytes = 1 << 20
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "application/json" && mediaType != "text/event-stream") {
		return nil, fmt.Errorf("unsupported MCP content type %q", contentType)
	}
	decode := func(data []byte) (json.RawMessage, error) {
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int            `json:"id"`
			Method  string          `json:"method"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("decode MCP response: %w", err)
		}
		if message.JSONRPC != "2.0" {
			return nil, fmt.Errorf("unexpected JSON-RPC version %q", message.JSONRPC)
		}
		if message.ID == nil {
			if message.Method != "" {
				return nil, nil
			}
			return nil, fmt.Errorf("missing JSON-RPC id")
		}
		if *message.ID != id {
			return nil, nil
		}
		if message.Error != nil {
			if message.Result != nil {
				return nil, fmt.Errorf("MCP response contains both result and error")
			}
			return nil, fmt.Errorf("JSON-RPC error %d", message.Error.Code)
		}
		if message.Result == nil {
			return nil, fmt.Errorf("MCP response missing result")
		}
		return message.Result, nil
	}
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	tooLarge := fmt.Errorf("MCP response exceeds %d bytes", maxBytes)
	if mediaType == "application/json" {
		data, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("read MCP response: %w", err)
		}
		if len(data) > maxBytes {
			return nil, tooLarge
		}
		if result, err := decode(data); result != nil || err != nil {
			return result, err
		}
	} else {
		scanner := bufio.NewScanner(limited)
		scanner.Buffer(make([]byte, 4096), maxBytes+1)
		var data []string
		for scanner.Scan() {
			if limited.N == 0 {
				return nil, tooLarge
			}
			line := scanner.Text()
			if line == "" && len(data) > 0 {
				if result, err := decode([]byte(strings.Join(data, "\n"))); result != nil || err != nil {
					return result, err
				}
				data = nil
			} else if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(value, " "))
			}
		}
		if limited.N == 0 {
			return nil, tooLarge
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read MCP event stream: %w", err)
		}
	}
	return nil, fmt.Errorf("no response for JSON-RPC id %d", id)
}

func TestParseMCPResponse(t *testing.T) {
	const result = `{"content":[{"type":"text","text":"unique-echo"}]}`
	const response = `{"jsonrpc":"2.0","id":3,"result":` + result + `}`
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
		wantError   string
	}{
		{name: "JSON", contentType: "application/json; charset=utf-8", body: response, want: result},
		{name: "SSE", contentType: "text/event-stream", body: "event: message\ndata: " + response + "\n\n", want: result},
		{name: "SSE multiple events", contentType: "text/event-stream", body: ": keepalive\r\n\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\r\n\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\r\n\r\nid: stream-event\r\nevent: message\r\ndata: " + response + "\r\n\r\n", want: result},
		{name: "SSE multiline data", contentType: "text/event-stream; charset=utf-8", body: "data: {\"jsonrpc\":\"2.0\",\n" + "data: \"id\":3,\"result\":" + result + "}\n\n", want: result},
		{name: "SSE no space after colon", contentType: "text/event-stream", body: "data:" + response + "\n\n", want: result},
		{name: "null result", contentType: "application/json", body: `{"jsonrpc":"2.0","id":3,"result":null}`, want: "null"},
		{name: "malformed JSON", contentType: "application/json", body: "{", wantError: "decode MCP response"},
		{name: "malformed SSE", contentType: "text/event-stream", body: "data: not-json\n\n", wantError: "decode MCP response"},
		{name: "JSON id mismatch", contentType: "application/json", body: `{"jsonrpc":"2.0","id":2,"result":{}}`, wantError: "no response for JSON-RPC id 3"},
		{name: "SSE id mismatch", contentType: "text/event-stream", body: "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\n\n", wantError: "no response for JSON-RPC id 3"},
		{name: "string id", contentType: "application/json", body: `{"jsonrpc":"2.0","id":"3","result":{}}`, wantError: "decode MCP response"},
		{name: "JSON RPC error", contentType: "application/json", body: `{"jsonrpc":"2.0","id":3,"error":{"code":-32603,"message":"internal error"}}`, wantError: "JSON-RPC error -32603"},
		{name: "SSE RPC error", contentType: "text/event-stream", body: "data: {\"jsonrpc\":\"2.0\",\"id\":3,\"error\":{\"code\":-32603,\"message\":\"internal error\"}}\n\n", wantError: "JSON-RPC error -32603"},
		{name: "missing id", contentType: "application/json", body: `{"jsonrpc":"2.0","result":{}}`, wantError: "missing JSON-RPC id"},
		{name: "wrong version", contentType: "application/json", body: `{"jsonrpc":"1.0","id":3,"result":{}}`, wantError: "JSON-RPC version"},
		{name: "missing result", contentType: "application/json", body: `{"jsonrpc":"2.0","id":3}`, wantError: "missing result"},
		{name: "result and error", contentType: "application/json", body: `{"jsonrpc":"2.0","id":3,"result":{},"error":{"code":-32603}}`, wantError: "both result and error"},
		{name: "trailing JSON", contentType: "application/json", body: response + response, wantError: "decode MCP response"},
		{name: "empty stream", contentType: "text/event-stream", body: ": keepalive\n\n", wantError: "no response for JSON-RPC id 3"},
		{name: "unsupported content type", contentType: "text/html", body: response, wantError: "MCP content type"},
		{name: "oversized JSON", contentType: "application/json", body: strings.Repeat(" ", (1<<20)+1), wantError: "exceeds"},
		{name: "oversized SSE", contentType: "text/event-stream", body: strings.Repeat(": keepalive\n\n", 100000), wantError: "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMCPResponse(strings.NewReader(tt.body), tt.contentType, 3)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("parseMCPResponse() error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMCPResponse() error = %v", err)
			}
			if !json.Valid(got) || string(got) != tt.want {
				t.Fatalf("parseMCPResponse() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseMCPResponseOpenStream(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		reader.Close()
		writer.Close()
	})
	go func() {
		// Deliberately leave the stream open after the complete response event.
		_, _ = io.WriteString(writer, "data: {\"jsonrpc\":\"2.0\",\"id\":3,\"result\":\"echo\"}\n\n")
	}()
	type response struct {
		result json.RawMessage
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := parseMCPResponse(reader, "text/event-stream", 3)
		done <- response{result, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || string(got.result) != `"echo"` {
			t.Fatalf("open stream response = %s, error = %v", got.result, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parser waited for EOF instead of returning the complete SSE response")
	}
}
