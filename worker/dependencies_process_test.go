package worker

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/azure/azure-functions-golang-worker/worker/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestDependencyInventoryCompiledProcess(t *testing.T) {
	var baseline string
	checkSame := func(t *testing.T, inventory string) {
		t.Helper()
		if baseline == "" {
			baseline = inventory
		} else if inventory != baseline {
			t.Fatal("normal/stripped or direct/proxy inventories differ")
		}
	}
	for _, variant := range []struct {
		name  string
		flags []string
	}{
		{name: "normal"},
		{name: "stripped", flags: []string{"-trimpath", "-ldflags=-s -w"}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			app := buildInventoryExecutable(t, "./worker/testdata/dependencyinventory", variant.flags)
			t.Run("direct", func(t *testing.T) {
				checkSame(t, testInventoryProcess(t, app, app, false))
			})
			t.Run("flex_proxy", func(t *testing.T) {
				if runtime.GOOS != "linux" {
					t.Skip("Flex proxy uses syscall.Exec; its executable is tested only on Linux")
				}
				proxy := buildInventoryExecutable(t, "./proxy", nil)
				checkSame(t, testInventoryProcess(t, proxy, app, true))
			})
		})
	}
}

func buildInventoryExecutable(t *testing.T, pkg string, flags []string) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	name := "inventory-app"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	buildDir := root
	if pkg == "./worker/testdata/dependencyinventory" {
		// A private, app-only module makes the proxy check discriminating:
		// reporting the proxy's own BuildInfo must not satisfy the assertion.
		buildDir = t.TempDir()
		fixture, err := os.ReadFile(filepath.Join(root, pkg, "main.go"))
		if err != nil {
			t.Fatal(err)
		}
		fixture = bytes.Replace(fixture, []byte("import ("), []byte("import (\n\t_ \"private.example/inventory/customer\""), 1)
		sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{
			"main.go":              fixture,
			"go.sum":               sums,
			"go.mod":               []byte(fmt.Sprintf("module inventory-test-app\n\ngo 1.25.0\n\nrequire (\n%s v0.0.0\nprivate.example/inventory/customer v1.2.3\n)\n\nreplace %s => %q\nreplace private.example/inventory/customer => ./customer\n", sdkModulePath, sdkModulePath, filepath.ToSlash(root))),
			"customer/go.mod":      []byte("module private.example/inventory/customer\n\ngo 1.25.0\n"),
			"customer/customer.go": []byte("package customer\n"),
		}
		for path, data := range files {
			target := filepath.Join(buildDir, path)
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		pkg = "."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := append([]string{"build", "-buildvcs=false", "-o", binary}, flags...)
	if buildDir != root {
		args = append(args, "-mod=mod")
	}
	cmd := exec.CommandContext(ctx, "go", append(args, pkg)...)
	cmd.Dir = buildDir
	if buildDir != root {
		cmd.Env = append(os.Environ(), "GOWORK=off")
	}
	cmd.WaitDelay = 5 * time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
	return binary
}

type inventoryHostMessage struct {
	message *pb.StreamingMessage
	err     error
}

type inventoryProcessHost struct {
	pb.UnimplementedFunctionRpcServer
	connected chan pb.FunctionRpc_EventStreamServer
	messages  chan inventoryHostMessage
}

func (h *inventoryProcessHost) EventStream(stream pb.FunctionRpc_EventStreamServer) error {
	select {
	case h.connected <- stream:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	for {
		message, err := stream.Recv()
		select {
		case h.messages <- inventoryHostMessage{message: message, err: err}:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		if err != nil {
			return err
		}
	}
}

func testInventoryProcess(t *testing.T, executable, app string, proxy bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := &inventoryProcessHost{
		connected: make(chan pb.FunctionRpc_EventStreamServer, 1),
		messages:  make(chan inventoryHostMessage, 64),
	}
	server := grpc.NewServer()
	pb.RegisterFunctionRpcServer(server, host)
	stopOnTimeout := context.AfterFunc(ctx, server.Stop)
	defer stopOnTimeout()
	defer server.Stop()
	go func() { _ = server.Serve(listener) }()

	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	deadline, _ := ctx.Deadline()
	if err := control.(*net.TCPListener).SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, executable,
		"--functions-uri", "http://"+listener.Addr().String(),
		"--functions-worker-id", "inventory-worker",
		"--functions-request-id", "inventory-start")
	cmd.Dir = filepath.Dir(app)
	cmd.Env = append(os.Environ(),
		"DEPENDENCY_INVENTORY_CONTROL="+control.Addr().String(),
		"WEBSITE_PLACEHOLDER_MODE=1",
		// Prevent the proxy's startup exec bypass from finding an existing app.
		"FUNCTIONS_APP_BINARY_NAME=dependency-inventory-placeholder-does-not-exist")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// This defer runs after the cooperative child shutdown below, including on
	// assertion failures. Reading output only after Wait avoids a buffer race.
	defer func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("process exit: %v\n%s", err, output.String())
			}
		case <-time.After(5 * time.Second):
			cancel()
			err := <-done
			t.Errorf("process required forced cleanup: %v\n%s", err, output.String())
		}
		if t.Failed() {
			t.Logf("process output:\n%s", output.String())
		}
	}()
	var child net.Conn
	var pid int
	defer func() {
		// Accept even on an early handshake failure so an already-started
		// proxy child gets the cooperative shutdown rather than being orphaned.
		if child == nil {
			_ = control.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
			child, _ = control.Accept()
		}
		if child == nil {
			return
		}
		defer child.Close()
		_ = child.(*net.TCPConn).CloseWrite()
		_ = child.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.Copy(io.Discard, child); err != nil {
			if pid > 0 {
				process, findErr := os.FindProcess(pid)
				if findErr == nil {
					_ = process.Kill()
					_ = process.Release()
				}
			}
			t.Errorf("fixture required forced cleanup: %v", err)
		}
	}()

	var stream pb.FunctionRpc_EventStreamServer
	select {
	case stream = <-host.connected:
	case <-ctx.Done():
		t.Fatal("timed out waiting for host connection")
	}
	var logs []*pb.RpcLog
	next := func(requestID string) *pb.StreamingMessage {
		t.Helper()
		for {
			select {
			case result := <-host.messages:
				if result.err != nil {
					t.Fatalf("host receive: %v", result.err)
				}
				if log := result.message.GetRpcLog(); log != nil {
					logs = append(logs, log)
					continue
				}
				if requestID != "" && result.message.GetRequestId() != requestID {
					t.Fatalf("response request ID = %q, want %q", result.message.GetRequestId(), requestID)
				}
				return result.message
			case <-ctx.Done():
				t.Fatal("timed out waiting for worker response")
			}
		}
	}
	send := func(message *pb.StreamingMessage) {
		t.Helper()
		if err := stream.Send(message); err != nil {
			t.Fatalf("host send: %v", err)
		}
	}
	if start := next("").GetStartStream(); start == nil || start.GetWorkerId() != "inventory-worker" {
		t.Fatalf("unexpected StartStream: %v", start)
	}
	send(&pb.StreamingMessage{RequestId: "inventory-start", Content: &pb.StreamingMessage_WorkerInitRequest{
		WorkerInitRequest: &pb.WorkerInitRequest{},
	}})
	init := next("inventory-start").GetWorkerInitResponse()
	if init == nil || init.GetResult() == nil || init.GetResult().GetStatus() != pb.StatusResult_Success {
		t.Fatalf("unsuccessful WorkerInitResponse: %v", init)
	}
	metadata := init.GetWorkerMetadata()
	if proxy {
		if _, ok := metadata.GetCustomProperties()["app_dependencies"]; ok {
			t.Fatal("placeholder must not advertise an app dependency inventory")
		}
		send(&pb.StreamingMessage{RequestId: "inventory-reload", Content: &pb.StreamingMessage_FunctionEnvironmentReloadRequest{
			FunctionEnvironmentReloadRequest: &pb.FunctionEnvironmentReloadRequest{
				FunctionAppDirectory: filepath.Dir(app),
				EnvironmentVariables: map[string]string{
					"FUNCTIONS_APP_BINARY_NAME": filepath.Base(app),
					"WEBSITE_PLACEHOLDER_MODE":  "0",
				},
			},
		}})
	}

	// The fixture connects before worker.Start. Keep its control socket until
	// cleanup so it can exit even if the proxy dies or gRPC teardown hangs.
	child, err = control.Accept()
	if err != nil {
		t.Fatalf("fixture control connection: %v", err)
	}
	if err := child.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fscanln(bufio.NewReader(child), &pid); err != nil || pid <= 0 {
		t.Fatalf("fixture PID: %d, error: %v", pid, err)
	}
	if (pid == cmd.Process.Pid) == proxy {
		t.Fatalf("fixture PID = %d, launched PID = %d, proxy = %t", pid, cmd.Process.Pid, proxy)
	}
	if proxy {
		reload := next("inventory-reload").GetFunctionEnvironmentReloadResponse()
		if reload == nil || reload.GetResult() == nil || reload.GetResult().GetStatus() != pb.StatusResult_Success {
			t.Fatalf("unsuccessful FunctionEnvironmentReloadResponse: %v", reload)
		}
		metadata = reload.GetWorkerMetadata()
	}
	assertExecutableInventory(t, app, metadata)
	if !proxy {
		// Control responses use the worker startup request ID in the existing
		// dispatcher. The proxy separately echoes its specialization request ID.
		send(&pb.StreamingMessage{RequestId: "inventory-start", Content: &pb.StreamingMessage_FunctionEnvironmentReloadRequest{
			FunctionEnvironmentReloadRequest: &pb.FunctionEnvironmentReloadRequest{},
		}})
		reload := next("inventory-start").GetFunctionEnvironmentReloadResponse()
		if reload.GetWorkerMetadata().GetCustomProperties()["app_dependencies"] != metadata.GetCustomProperties()["app_dependencies"] {
			t.Fatal("compiled app init and reload inventories differ")
		}
	}

	// A subsequent response is a queue barrier, avoiding a sleep-based negative
	// log assertion. The startup log must have arrived before this response.
	send(&pb.StreamingMessage{RequestId: "inventory-start", Content: &pb.StreamingMessage_WorkerStatusRequest{
		WorkerStatusRequest: &pb.WorkerStatusRequest{},
	}})
	if next("inventory-start").GetWorkerStatusResponse() == nil {
		t.Fatal("expected WorkerStatusResponse after initialization")
	}
	startupSeen := false
	for _, log := range logs {
		encoded, err := protojson.Marshal(log)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(log.GetMessage(), "Go worker started") {
			startupSeen = true
		}
		for _, excluded := range []string{"app_dependencies", "private.example/inventory/customer", "golang.org/x/text", "schema_version"} {
			if bytes.Contains(encoded, []byte(excluded)) {
				t.Errorf("RpcLog contains inventory value %q: %s", excluded, encoded)
			}
		}
	}
	if !startupSeen {
		t.Fatal("did not observe startup RpcLog; log exclusion assertion would be vacuous")
	}
	return metadata.GetCustomProperties()["app_dependencies"]
}

func assertExecutableInventory(t *testing.T, executable string, metadata *pb.WorkerMetadata) {
	t.Helper()
	if metadata == nil {
		t.Fatal("response is missing WorkerMetadata")
	}
	info, err := buildinfo.ReadFile(executable)
	if err != nil {
		t.Fatalf("read executable build info: %v", err)
	}
	got := readInventory(t, metadata)
	if got.Status != "available" || got.Total != len(info.Deps) || got.Reported != len(info.Deps) || got.Truncated {
		t.Fatalf("inventory = %+v; executable has %d dependencies", got, len(info.Deps))
	}
	// Compare every wire record to the executable using independent test types.
	// The two explicit checks below also pin the meaning of local replacements
	// and establish that the app-only dependency really exists.
	expected := make([]moduleWire, 0, len(info.Deps))
	for _, dep := range info.Deps {
		m := moduleWire{Path: dep.Path, Version: dep.Version}
		if r := dep.Replace; r != nil {
			m.Replacement = &replacementWire{}
			switch r.Version {
			case "", "(devel)":
				m.Version = ""
			default:
				m.Replacement = &replacementWire{Path: r.Path, Version: r.Version}
			}
		}
		expected = append(expected, m)
	}
	slices.SortFunc(expected, func(a, b moduleWire) int {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return bytes.Compare(left, right)
	})
	if !reflect.DeepEqual(got.Modules, expected) {
		t.Fatalf("inventory records = %+v, want executable records %+v", got.Modules, expected)
	}
	privateFound := false
	for _, dep := range got.Modules {
		if dep.Path == "private.example/inventory/customer" {
			privateFound = true
			if dep.Version != "" || dep.Replacement == nil || *dep.Replacement != (replacementWire{}) {
				t.Fatalf("local replacement should have unknown version and no directory: %+v", dep)
			}
		}
	}
	if !privateFound {
		t.Fatal("missing private app-only dependency; inventory may describe the proxy instead of the app")
	}
	const knownModule = "golang.org/x/text"
	version := ""
	for _, dep := range info.Deps {
		if dep.Path == knownModule {
			version = dep.Version
		}
	}
	if version == "" {
		t.Fatalf("compiled fixture does not contain versioned %s", knownModule)
	}
	for _, dep := range got.Modules {
		if dep.Path == knownModule {
			if dep.Version != version || dep.Replacement != nil {
				t.Fatalf("reported module = %+v, executable version = %q", dep, version)
			}
			return
		}
	}
	t.Fatalf("inventory does not contain app dependency %s", knownModule)
}
