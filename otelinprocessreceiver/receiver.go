package otelinprocessreceiver

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
)

// Type is the collector component type for this receiver. Reference it as
// "inprocess" in collector config and when registering the factory.
var Type = component.MustNewType("inprocess")

// Config is the (empty) receiver configuration. The receiver has no settings —
// its data source is the embedding application, not anything configurable.
type Config struct{}

// Receiver is an in-process logs receiver. The embedding application registers
// its Factory with the collector, then calls ConsumeLogs to push pdata straight
// into the pipeline. A single Receiver backs one pipeline.
type Receiver struct {
	once sync.Once
	done chan struct{}   // closed once logs is captured
	logs consumer.Logs   // pipeline head consumer; set before done is closed
}

// New returns a Receiver ready to register and consume into.
func New() *Receiver {
	return &Receiver{done: make(chan struct{})}
}

// Factory returns the receiver.Factory to register under
// collector factories (Receivers[Type]).
func (r *Receiver) Factory() receiver.Factory {
	return receiver.NewFactory(
		Type,
		func() component.Config { return &Config{} },
		receiver.WithLogs(r.createLogs, component.StabilityLevelDevelopment),
	)
}

// createLogs is invoked once while the collector builds the logs pipeline. The
// collector supplies next — the consumer at the head of the pipeline — which we
// capture for ConsumeLogs.
func (r *Receiver) createLogs(_ context.Context, _ receiver.Settings, _ component.Config, next consumer.Logs) (receiver.Logs, error) {
	r.once.Do(func() {
		r.logs = next
		close(r.done)
	})
	return noopComponent{}, nil
}

// ConsumeLogs pushes logs into the pipeline. It blocks until the collector has
// built the pipeline (so an early call before the collector starts waits rather
// than failing), or until ctx is cancelled. Safe for concurrent use.
func (r *Receiver) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	select {
	case <-r.done:
		return r.logs.ConsumeLogs(ctx, ld)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// noopComponent is the component returned to the collector: lifecycle is a
// no-op because consumption is driven by the embedding application calling
// ConsumeLogs, not by the receiver.
type noopComponent struct{}

func (noopComponent) Start(context.Context, component.Host) error { return nil }
func (noopComponent) Shutdown(context.Context) error             { return nil }
