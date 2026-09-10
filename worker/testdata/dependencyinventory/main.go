package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
	"golang.org/x/text/language"
)

func main() {
	// Bound the fixture even if its parent or the proxy exits unexpectedly.
	time.AfterFunc(45*time.Second, func() { os.Exit(124) })
	control, err := net.DialTimeout("tcp", os.Getenv("DEPENDENCY_INVENTORY_CONTROL"), 5*time.Second)
	if err != nil {
		panic(err)
	}
	defer control.Close()
	if _, err := fmt.Fprintln(control, os.Getpid()); err != nil {
		panic(err)
	}
	go func() {
		// Closing the test-owned connection also stops a child behind a proxy.
		_, _ = io.Copy(io.Discard, control)
		// Close explicitly before Exit so Windows sends FIN instead of resetting
		// the socket during process teardown.
		_ = control.Close()
		os.Exit(0)
	}()

	// Exercise an external package in the compiled app, not just in its tests.
	if language.Make("en-US").String() != "en-US" {
		panic("unexpected language tag")
	}
	worker.Start(sdk.FunctionApp())
}
