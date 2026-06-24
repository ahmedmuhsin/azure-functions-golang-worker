package otelinprocessreceiver

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
)

func TestFactory_Type(t *testing.T) {
	r := New()
	if got := r.Factory().Type(); got != Type {
		t.Errorf("factory type = %v, want %v", got, Type)
	}
}

// captureConsumer records the logs it receives, standing in for the pipeline
// head consumer the collector would supply.
type captureConsumer struct{ last plog.Logs }

func (c *captureConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}
func (c *captureConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	c.last = ld
	return nil
}

func TestConsumeLogs_DelegatesAfterCreate(t *testing.T) {
	r := New()
	cap := &captureConsumer{}

	// Simulate the collector building the pipeline: createLogs captures the
	// consumer the same way the collector would.
	if _, err := r.createLogs(context.Background(), receiver.Settings{}, &Config{}, cap); err != nil {
		t.Fatalf("createLogs: %v", err)
	}

	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("hi")
	if err := r.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	if cap.last.LogRecordCount() != 1 {
		t.Errorf("delegated record count = %d, want 1", cap.last.LogRecordCount())
	}
}

func TestConsumeLogs_BlocksUntilReady(t *testing.T) {
	r := New()
	cap := &captureConsumer{}

	// Start a consumer call before the pipeline is built; it must block.
	errCh := make(chan error, 1)
	go func() {
		ld := plog.NewLogs()
		ld.ResourceLogs().AppendEmpty()
		errCh <- r.ConsumeLogs(context.Background(), ld)
	}()

	select {
	case <-errCh:
		t.Fatal("ConsumeLogs returned before the pipeline was built")
	case <-time.After(50 * time.Millisecond):
		// expected: still blocked
	}

	// Now build the pipeline; the blocked call should complete.
	if _, err := r.createLogs(context.Background(), receiver.Settings{}, &Config{}, cap); err != nil {
		t.Fatalf("createLogs: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ConsumeLogs after ready: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConsumeLogs did not unblock after pipeline build")
	}
}

func TestConsumeLogs_ContextCancelledWhileWaiting(t *testing.T) {
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.ConsumeLogs(ctx, plog.NewLogs()); err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}
