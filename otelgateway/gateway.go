package otelgateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultOTLPEndpoint is the loopback address the embedded collector's bundled
// otlp receiver listens on (see otelcollector). Pass it to [New] when the
// gateway exports to a worker-managed embedded collector.
const DefaultOTLPEndpoint = "localhost:4317"

// LogsConsumer is the sink a gateway pushes decoded logs into when it bypasses
// OTLP. It is satisfied structurally by the collector's consumer.Logs and by
// otelinprocessreceiver.Receiver, so [NewWithConsumer] can hand pdata straight
// into an embedded collector's pipeline with no serialization or loopback. The
// interface is declared here so otelgateway takes no collector ingress
// dependency.
type LogsConsumer interface {
	ConsumeLogs(ctx context.Context, ld plog.Logs) error
}

// logsSink is the gateway's egress: either OTLP export or a direct consumer.
type logsSink interface {
	consume(ctx context.Context, ld plog.Logs) error
}

type otlpSink struct {
	client plogotlp.GRPCClient
	conn   *grpc.ClientConn
}

func (s *otlpSink) consume(ctx context.Context, ld plog.Logs) error {
	_, err := s.client.Export(ctx, plogotlp.NewExportRequestFromLogs(ld))
	return err
}

type consumerSink struct{ c LogsConsumer }

func (s *consumerSink) consume(ctx context.Context, ld plog.Logs) error {
	return s.c.ConsumeLogs(ctx, ld)
}

// Gateway decodes Azure logs from Event Hub messages and forwards them to a
// collector — over OTLP ([New]) or directly into an embedded pipeline
// ([NewWithConsumer]). One Gateway is shared across every binding in an app;
// each binding selects its encoding via [Gateway.Handler].
type Gateway struct {
	sink      logsSink
	conn      *grpc.ClientConn // non-nil only for the OTLP sink; closed by Close
	encodings map[string]plog.Unmarshaler
	enrich    bool
}

// Option configures a [Gateway].
type Option func(*Gateway)

// WithEncoding registers an additional named encoding (a plog.Unmarshaler that
// turns an Event Hub message body into logs). It overrides a built-in of the
// same name. This is the library equivalent of the receiver's pluggable
// encoding extensions.
func WithEncoding(name string, u plog.Unmarshaler) Option {
	return func(g *Gateway) { g.encodings[name] = u }
}

// WithMetadataEnrichment folds Event Hub invoke metadata (partition key,
// offset, enqueued time, sequence number, system properties) onto the resource
// attributes of every exported log. Mirrors the receiver's include_metadata.
func WithMetadataEnrichment() Option {
	return func(g *Gateway) { g.enrich = true }
}

// New dials the OTLP endpoint and returns a Gateway preloaded with the built-in
// encodings ([EncodingResourceLogs], [EncodingRaw]). An empty endpoint defaults
// to [DefaultOTLPEndpoint]. The endpoint may be a host:port or a URL; the gRPC
// target is taken from its host. grpc.NewClient is lazy, so New does not block
// on the collector being up.
//
// Use this for an external collector, or for an embedded collector reached over
// OTLP loopback. For the lowest-latency embedded path (no serialization), see
// [NewWithConsumer].
func New(endpoint string, opts ...Option) (*Gateway, error) {
	if endpoint == "" {
		endpoint = DefaultOTLPEndpoint
	}
	conn, err := grpc.NewClient(grpcTarget(endpoint), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("otelgateway: dial OTLP endpoint %q: %w", endpoint, err)
	}
	g := newGateway(&otlpSink{client: plogotlp.NewGRPCClient(conn), conn: conn}, opts...)
	g.conn = conn
	return g, nil
}

// NewWithConsumer returns a Gateway that pushes decoded logs straight into the
// given consumer instead of exporting over OTLP. Paired with an embedded
// collector and otelinprocessreceiver, this is the in-memory path: no marshal,
// no loopback. The consumer is typically an otelinprocessreceiver.Receiver,
// whose ConsumeLogs blocks until the embedded pipeline is built.
func NewWithConsumer(c LogsConsumer, opts ...Option) *Gateway {
	return newGateway(&consumerSink{c: c}, opts...)
}

func newGateway(sink logsSink, opts ...Option) *Gateway {
	g := &Gateway{
		sink:      sink,
		encodings: builtinEncodings(),
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Handler returns an Event Hub handler bound to a named encoding: it decodes
// the message with that encoding, optionally enriches it, and forwards it to
// the gateway's sink. A forward error fails the invocation so the host retries.
//
// Handler panics if encoding is not registered — a setup-time programming error
// surfaced at startup, consistent with the worker SDK's handler validation.
// Register custom encodings with [WithEncoding].
func (g *Gateway) Handler(encoding string) sdk.EventHubHandler {
	u, ok := g.encodings[encoding]
	if !ok {
		panic(fmt.Sprintf("otelgateway: unknown encoding %q (register it with WithEncoding)", encoding))
	}
	return func(ctx context.Context, msg bindings.EventHubMessage) error {
		ld, err := u.UnmarshalLogs(msg.Body)
		if err != nil {
			return err
		}
		if ld.LogRecordCount() == 0 {
			return nil
		}
		if g.enrich {
			enrichResourceAttributes(ld, msg)
		}
		slog.DebugContext(ctx, "otelgateway forwarding logs",
			"encoding", encoding,
			"records", ld.LogRecordCount(),
			"resources", ld.ResourceLogs().Len(),
		)
		return g.sink.consume(ctx, ld)
	}
}

// Close releases the underlying OTLP connection, if any. It is a no-op for a
// consumer-backed gateway ([NewWithConsumer]).
func (g *Gateway) Close() error {
	if g.conn != nil {
		return g.conn.Close()
	}
	return nil
}

// grpcTarget normalizes an OTLP endpoint (URL or host:port) to the host:port
// form grpc.NewClient expects.
func grpcTarget(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return endpoint
}
