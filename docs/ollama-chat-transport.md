# Ollama chat transport

Ollama channels (type `4`) expose **Use OpenAI-compatible chat API** in channel
settings. It defaults to off, so existing channels need no migration. The option
only changes ordinary OpenAI chat requests, including chat playground requests.

| Request | Off | On |
| --- | --- | --- |
| OpenAI Chat Completions | `/api/chat` | `/v1/chat/completions` |
| Completions | `/api/generate` | `/api/generate` |
| Embeddings | `/api/embed` | `/api/embed` |
| Responses | `/v1/responses` | `/v1/responses` |
| Claude Messages with body passthrough | `/v1/messages` | `/v1/messages` |
| Claude Messages without body passthrough | Existing native conversion to `/api/chat` | Existing native conversion to `/api/chat` |

The flag is saved in the channel's `settings` JSON, alongside other optional
provider settings. It is independent of `setting` and Advanced Custom routes:

```json
{"ollama_openai_chat": true}
```

In Go, enabled chat uses the existing OpenAI request converter and response
handlers for JSON and SSE. Converted streaming requests ask Ollama for usage
events for accounting, while the client's choice to hide downstream usage is
preserved. Body passthrough retains the original request body. Model discovery,
pulling and deletion continue to use Ollama's native management API.

Rust channel management preserves the saved settings JSON. Its relay supports
the explicitly enabled OpenAI chat transport and leaves existing Responses
handling independent of this flag. Rust does not implement native Ollama chat
or generate conversion: unset/disabled chat and Completions fail with
`ollama_native_transport_unsupported` (HTTP 501), before quota reservation or
upstream I/O. Use the Go relay for those native paths.
