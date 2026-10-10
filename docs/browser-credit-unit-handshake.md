# Browser credit-unit acknowledgement

The canonical Web client sends `X-LMM-Credit-Unit: 500000` through its shared
HTTP request interceptor. The value describes this client's fixed
500,000-credit/USD input contract. It is independent of persisted status metadata
and cannot be overridden by a request caller. Cached pre-transition currency
rates remain invalid until a fresh status response supplies the canonical basis.
The assistant chat and Ollama model-pull streaming fetches declare the same
header while preserving their authentication, abort signal and event stream.

After dashboard authentication succeeds, Go requires this acknowledgement on
unsafe `/api/*` dashboard requests identified as browser requests by `Origin` or
`Sec-Fetch-Site`. A missing or different value returns HTTP 409 with
`code: CREDIT_UNIT_REFRESH_REQUIRED` and a message asking the user to refresh.
The mutation does not run. An old open Web119/120 tab therefore cannot submit
quota converted using its cached old denomination.

The acknowledgement is a compatibility declaration, not authentication or an
authorization credential. Direct non-browser raw API clients, provider callbacks
and safe reads retain their existing behavior. Raw quota bodies remain integer
wallet points; the handshake never rescales a request.

The Web client surfaces the 409 message without replaying the mutation,
refreshing authentication, clearing the login, or automatically reloading the
page. Once the canonical frontend is activated normally, a page refresh loads
the new client while preserving the user's session. The maintenance deployment
continues to freeze its frontend with `WebChanged=false`; the handshake protects
the gap before normal Web activation without changing that release contract.
