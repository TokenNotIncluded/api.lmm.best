# OpenAI native image, moderation, and voice models

Use the existing **OpenAI** channel with its official key. Compatible Relay
channels can forward these native protocols when their upstream also implements
them. No extra channel type is needed for these OpenAI models. Jev has its own
TypeSafe channel; see [Jev native relay](jev-native-relay.md).

## Prices

Deploy both backend and frontend before saving these six `tiered_expr` prices.
Keep existing entries in `billing_setting.billing_mode` and
`billing_setting.billing_expr`; replace only these model entries. Group prices
continue to apply their existing multiplier.

| Model | Expression |
| --- | --- |
| `chatgpt-image-latest` | `tier("base", p*5 + c*10 + img*8 + img_o*32 + cr_text*1.25 + cr_img*2)` |
| `gpt-image-1-mini` | `tier("base", p*2 + img*2.5 + img_o*8 + cr_text*0.2 + cr_img*0.25)` |
| `gpt-live-1` | `tier("base", audio_s * 0.05 * 1000000 / 60)` |
| `gpt-live-transcribe` | `tier("base", audio_s * 0.017 * 1000000 / 60)` |
| `gpt-realtime-whisper` | `tier("base", audio_s * 0.017 * 1000000 / 60)` |
| `gpt-realtime-translate` | `tier("base", audio_s * 0.034 * 1000000 / 60)` |

Image coefficients are USD per million tokens. The expression engine divides
the resulting value by one million exactly once. `audio_s` is seconds, so the
duration expressions convert the official per-minute prices into that same
expression unit. The editor displays duration prices in USD/minute.

`p` and `img` exclude separately priced cache reads; text and image cache are
not subtracted twice. Native Images `output_tokens` are image tokens when the
provider omits output details, as defined by that endpoint's schema. Explicit
output details remain authoritative.

Missing cache classification is not a measured zero. Zero aggregate cache use
requires no classification to calculate these prices. Positive cache use
without valid modality details cannot be priced accurately with these
expressions: the request fails without a final image or retry and refunds its
reservation. In a stream, a terminal error follows any already delivered
partial data. OpenAI's published Images schema does not currently promise a
cached-modality breakdown; real upstream acceptance of that case remains a
separate check.

Sources: [official prices](https://developers.openai.com/api/docs/pricing),
[Images response schema](https://developers.openai.com/api/reference/resources/images).

## Native interfaces

| Model | Gateway interface | Startup model field |
| --- | --- | --- |
| `gpt-live-1` | WebSocket `GET /v1/live/sessions` | `session.start.session.model` |
| `gpt-live-transcribe`, `gpt-realtime-whisper` | WebSocket `GET /v1/realtime?intent=transcription` | `session.update.session.audio.input.transcription.model` |
| `gpt-realtime-translate` | WebSocket `GET /v1/realtime/translations?model=...` | query `model` |

Transcription and translation accept PCM16 mono audio at 24 kHz. Transcription
currently uses manual commit with `turn_detection: null`. Repeated transcription
completion events are deduplicated. Live usage is cumulative: updates for 12
then 15 seconds settle 15 seconds. Translation has no documented upstream
duration meter, so its successful input audio bytes divided by 48,000 are
recorded as an **estimated input duration**. Wall-clock time and output audio
are not substituted for that measurement.

Models and request prices are frozen at startup, including channel aliases.
One billing session owns the reservation, ongoing budget, and final settlement.
Budget exhaustion stops further forwarding. A disconnect drains available
final usage; missing final usage and exceptional price evaluation fallbacks
remain explicit in the consume log. Default backend realtime also uses one
settlement owner, avoiding double charging after `response.done`.

Live WebRTC uses `POST /v1/live/sessions`. The gateway reserves the initial
15-second minimum, creates the session, and attaches its own usage sideband
before returning the SDP answer. The minimum is credited against total duration.
Startup locks data-channel permissions against model or hosted Responses
changes. WebRTC media and permitted direct data-channel text events bypass
gateway event inspection; startup JSON still receives security checks. A failed
attach requests termination and records any failure; it does not claim the
remote session has closed without evidence.

Hosted Responses delegation and optional extra translation transcription are
rejected until their separate costs can be metered. Unmetered translation
WebRTC and ephemeral credential endpoints return 501 before provider work.
Existing chat OAuth scopes do not grant these new native protocols. Background
channel tests report `skipped` rather than starting paid voice sessions or
mistakenly disabling their channels.

Sources: [Live guide](https://developers.openai.com/api/docs/guides/live-conversations),
[transcription guide](https://developers.openai.com/api/docs/guides/realtime-transcription),
[translation guide](https://developers.openai.com/api/docs/guides/realtime-translation).

## Moderation

`POST /v1/moderations` defaults to `omni-moderation-latest`, sends native
`model`/`input`, and preserves the result schema even when channel force-format
is enabled. It does not add chat parameters or a channel system prompt.
Moderation has no upstream token meter: it never invents historical token use
or a one-token minimum. Existing administrator fixed or expression pricing
still applies; the official free models should retain their configured zero
price. Both moderation models can be tested through the native backend test
entry, without a chat request.
