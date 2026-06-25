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

The handler ([src/handler/main.go](src/handler/main.go)) is a minimal HTTP
server that logs the exact JSON body the host POSTs for every invocation, then
returns an empty custom-handler response. Three functions read the **same**
37 byte blob:

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

The reference carries `Source: AzureStorageBlobs` plus a small content
descriptor. That is exactly the JSON a custom handler would parse to build its
own blob client and do a ranged read or skip the download. A helper library
could wrap that reference-to-client step so custom handler authors do not
hand-roll it.

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

> Verified on func core tools 4.12.0, extension bundle 4.34.0, Azurite, against
> the released host (no host changes).
