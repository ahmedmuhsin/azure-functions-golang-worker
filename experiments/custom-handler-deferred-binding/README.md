# Custom handler deferred binding (experiment)

This is **not** a Go worker sample. It is a small **custom handler** app used to
verify a single platform behavior end to end:

> With `supportsDeferredBinding` set on a blob binding in `function.json`, the
> Functions host delivers a **reference** to the blob (a `ParameterBindingData`
> payload), not the blob's bytes, even to a plain custom handler.

It backs the discussion about where the OpenTelemetry receiver should live. The
relevant conclusion: the memory win of getting a blob reference instead of the
full content is **not** unique to the Go worker. A custom handler can opt into
it too, via a function.json property. What the worker adds on top is ergonomics
(a typed client instead of reference JSON you parse yourself).

## What it does

The handler ([src/handler/main.go](src/handler/main.go)) is a small HTTP server
that, for every invocation, logs the exact JSON body the host POSTs and then
**acts on the binding**. When the host delivers a deferred reference, the
handler builds an `azblob` client from it and does a ranged read, proving a
plain custom handler can use the reference without the host ever loading the
full blob. The connection comes from the app's `AzureWebJobsStorage` setting,
the same way any binding resolves it, so there is no account or key hard-coded
in the handler. Three functions read the **same** 37 byte blob:

| Function | Binding | `supportsDeferredBinding` |
|---|---|---|
| `BlobTriggerDeferred` | blob trigger | true |
| `ReadDeferred` | blob input (HTTP triggered) | true |
| `ReadContent` | blob input (HTTP triggered) | false (control) |

## The result

Captured payloads (trimmed). Deferred bindings get a **reference**:

```jsonc
// BlobTriggerDeferred  (deferred trigger)
"myblob": { "Version": "1.0", "Source": "AzureStorageBlobs",
            "Content": { "Length": 92, "MediaType": "application/json" },
            "ContentType": "application/json" }
// + Metadata.Uri = "http://127.0.0.1:10000/devstoreaccount1/test-container/hello.txt"
//   (the 37 bytes of content are NOT in the payload)
```

```jsonc
// ReadDeferred  (deferred input binding) — same reference shape
"myblob": { "Version": "1.0", "Source": "AzureStorageBlobs", "Content": { ... } }
```

The control (deferred off) gets the **full content** inline:

```jsonc
// ReadContent  (non-deferred input binding)
"myblob": "\"THIS-IS-THE-FULL-BLOB-CONTENT-PAYLOAD\""
```

Same handler, same blob. The only difference is the function.json property, which
proves the host (not the handler) decides reference vs content.

## What the handler does with each

The reactions the handler logged (`--- handler reaction ---` in `captured.log`):

```text
# BlobTriggerDeferred (deferred trigger)
DEFERRED reference (Source="AzureStorageBlobs"): built a blob client from the
host-supplied reference using the app's AzureWebJobsStorage connection, read the
first 5 of 37 bytes via a range request ("THIS-"). The full blob was never
downloaded into the invocation.

# ReadDeferred (deferred input binding)
DEFERRED reference (Source="AzureStorageBlobs"), but no blob URL in the payload.
The trigger exposes Metadata.Uri; an input binding does not, so a handler here
would reconstruct the path from its binding template plus its own connection.

# ReadContent (non-deferred input binding, control)
NON-DEFERRED: host delivered the full content inline (39 bytes).
```

So the platform behavior splits two ways:

- **Deferred on vs off** decides reference vs content. That is the memory win,
  and it is the host's call, not the worker's.
- **Trigger vs input binding** decides how actionable the reference is over the
  custom-handler boundary. The trigger hands you `Metadata.Uri`, so you build a
  client directly (done here). A deferred input binding hands you the reference
  descriptor but not the URL, so a handler reconstructs the blob path from its
  binding template plus the app's AzureWebJobsStorage connection. A typed worker
  binding papers over that difference, which is the ergonomics the worker adds.

## How to run

Requires `func` core tools (v4.x), `go`, and Azurite.

```pwsh
# 1. start Azurite (skip the version check for the storage extension)
azurite --skipApiVersionCheck --silent --location ./_azurite `
  --blobPort 10000 --queuePort 10001 --tablePort 10002

# 2. build the binaries (handler.exe is the custom handler; uploader seeds a blob)
go build -o handler.exe ./src/handler
go build -o uploader.exe ./src/uploader

# 3. seed the blob, then start the host
./uploader.exe hello.txt
func start --port 7095
```

Then hit `http://localhost:7095/api/read-deferred/hello.txt` and
`http://localhost:7095/api/read-content/hello.txt`. The blob trigger fires on
its own. Each invocation's payload is appended to `captured.log`.

The storage connection lives in [local.settings.json](local.settings.json) under
`AzureWebJobsStorage`. The host passes it to the handler as an environment
variable, and the uploader reads the same file, so neither binary carries a
hard-coded account or key. It is set to the explicit Azurite dev connection
string, which is what `UseDevelopmentStorage=true` expands to, because the Go
storage SDK does not understand that shorthand.

> Verified on func core tools 4.12.0, extension bundle 4.34.0, Azurite, against
> the released host (no host changes).
