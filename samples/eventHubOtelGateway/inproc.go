package main

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

// inProcType is the component type id for the in-process logs receiver. It is
// referenced as `inproc` in the collector config YAML below.
var inProcType = component.MustNewType("inproc")

// logsTap is a custom receiver factory whose only job is to capture the
// consumer at the head of the collector's logs pipeline. That consumer is the
// exact same in-memory entry point a built-in receiver feeds via ConsumeLogs —
// so application code can hand pdata straight into the pipeline with no OTLP
// serialization and no loopback network hop.
//
// This is the supported extensibility path: we register the factory through
// otelcollector.WithFactories before the collector builds its graph, and the
// collector passes our create function the live nextConsumer. We never reach
// into the running collector.
type logsTap struct {
	ready chan consumer.Logs
}

func newLogsTap() *logsTap {
	return &logsTap{ready: make(chan consumer.Logs, 1)}
}

// factory returns the receiver.Factory to register under otelcollector
// DefaultFactories().Receivers.
func (t *logsTap) factory() receiver.Factory {
	return receiver.NewFactory(
		inProcType,
		func() component.Config { return &struct{}{} },
		receiver.WithLogs(t.createLogs, component.StabilityLevelDevelopment),
	)
}

// createLogs is invoked once while the collector builds the logs pipeline. The
// collector supplies next — the consumer at the head of the pipeline — which we
// hand back to application code.
func (t *logsTap) createLogs(_ context.Context, _ receiver.Settings, _ component.Config, next consumer.Logs) (receiver.Logs, error) {
	t.ready <- next
	return tapReceiver{}, nil
}

// consumer blocks until the pipeline has been built and returns its head
// consumer, or fails if ctx is cancelled first. The pipeline is built when the
// embedded collector starts (inside worker.Start, before serving), so the
// first invocation either gets the consumer immediately or waits briefly for
// startup to finish.
func (t *logsTap) consumer(ctx context.Context) (consumer.Logs, error) {
	select {
	case c := <-t.ready:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tapReceiver is a no-op component: consumption is driven by application code
// calling the captured consumer, not by receiver Start/Shutdown. The collector
// still expects a component back from the factory, so we return this.
type tapReceiver struct{}

func (tapReceiver) Start(context.Context, component.Host) error { return nil }
func (tapReceiver) Shutdown(context.Context) error             { return nil }
