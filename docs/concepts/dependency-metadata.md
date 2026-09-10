# Dependency metadata

The Go worker reports an inventory of build dependency modules in
`WorkerMetadata.CustomProperties["app_dependencies"]`. The inventory is included
in initialization and environment-reload responses, not invocation responses.
On Flex Consumption, the proxy forwards the application's initialization
metadata in its specialization response. The placeholder does not report an
application inventory.

The Functions host source records worker metadata under the system metric
`rpcworkerchannel.workerinitresponse.workermetadata`. This is separate from
application logs and OpenTelemetry export. Delivery, retention, and ingestion
limits depend on the host and platform configuration.

## Schema version 1

The property is a JSON string. Its decoded value looks like this:

```json
{
  "schema_version": 1,
  "status": "available",
  "total": 3,
  "reported": 3,
  "truncated": false,
  "modules": [
    { "path": "example.org/client", "version": "v1.2.3" },
    { "path": "example.org/local", "replacement": {} },
    {
      "path": "example.org/original",
      "version": "v1.0.0",
      "replacement": { "path": "example.org/fork", "version": "v1.1.0" }
    }
  ]
}
```

- `status` is `available` when Go build information is present, otherwise
  `unavailable`. An unavailable inventory has zero counts and an empty array.
  An available empty inventory is not the same as unavailable metadata.
- `total` is the number of distinct normalized records before truncation.
  Empty module paths and nil entries are ignored. Identical records are
  deduplicated; different recorded versions or replacements remain distinct.
- `reported` is the number of records in `modules`.
- `truncated` is true if any counted records were omitted due to the byte limit.
- Without `replacement`, `version` is the version recorded by Go. It is omitted
  if empty and may be `(devel)` for development builds.
- A nonempty `replacement` identifies the effective versioned replacement.
  The outer path and version identify the original module; consumers must use
  the replacement for the effective identity.
- An empty `replacement` object means a versionless local replacement. Its
  directory and original requested version are omitted because the original
  pin does not describe the code built from that directory. Both empty and
  `(devel)` replacement versions are treated this way.

Records are sorted by their serialized JSON representation, then a prefix is
selected to keep the complete property at or below 8,192 UTF-8 bytes. Records
are never split. A single oversized first record can therefore produce an
empty, truncated inventory. Counts and the envelope are included in this
limit. Additional escaping in host serialization and the rest of
`WorkerMetadata` are not included, so this is not a guarantee about downstream
ingestion limits.

## Collection and interpretation

The source is `runtime/debug.ReadBuildInfo().Deps`, not an installed-package
scan. There is no public-module allowlist. Private module paths can contain
organization or repository names and are included. The new inventory does not
include the main application's module path, module checksums, build settings,
or local replacement directories. Existing properties such as
`sdk_replace_path` and `app_vcs_revision` are not changed by this feature.

The inventory includes both direct and transitive modules that contributed
packages to the binary. It does not distinguish them or establish that a
particular API was executed. It is not a complete software bill of materials
for native libraries, Go plugins loaded later, or external services.

SDK dependencies remain in the data. When measuring library adoption, account
for the SDK dependency baseline without assuming that overlap proves a
customer did not also choose the same library. Aggregate by distinct app and
deployment rather than counting initialization events as separate apps.

Collection makes no network requests and does not require the Go toolchain or
source files on the deployed host. Missing build metadata does not fail
initialization. No dependency records are added to the existing customer-facing
`Go worker started` log.