// Package otelinprocessreceiver provides an OpenTelemetry Collector receiver
// that an embedding application pushes pdata into directly, in-process.
//
// Unlike every stock receiver — which owns its ingestion (scrape, listen on a
// socket, poll a queue) — this receiver exposes the consumer at the head of its
// pipeline so host code can call ConsumeLogs without crossing a process
// boundary or serializing. It is the "collector as a library" ingress: when you
// embed a collector in your own process and want to hand it pdata you already
// hold, this avoids the OTLP loopback (marshal + localhost round-trip) that an
// otlp receiver would otherwise require.
//
// # Usage
//
//	r := otelinprocessreceiver.New()
//
//	factories, _ := otelcollector.DefaultFactories()
//	factories.Receivers[otelinprocessreceiver.Type] = r.Factory()
//
//	// config YAML must list the receiver in a logs pipeline:
//	//   receivers: { inprocess: {} }
//	//   service: { pipelines: { logs: { receivers: [inprocess], ... } } }
//
//	// r implements ConsumeLogs; call it from app code once the collector runs.
//	_ = r.ConsumeLogs(ctx, ld)
//
// ConsumeLogs blocks until the collector has built the logs pipeline (the
// consumer is captured during component creation, which happens at collector
// start), then delegates. This is safe for concurrent callers.
//
// # Status
//
// This is a worker-repo component with no upstream equivalent: the collector
// component model assumes receivers own ingestion, so no contrib/core receiver
// accepts host-pushed pdata. It is an "upstream candidate" if the
// collector-as-a-library pattern is ever standardized, but until then it is
// maintained here. Keep it dependency-clean (collector core only) so a future
// move is a relocation, not a rewrite.
package otelinprocessreceiver
