# Merchant delivery templates

Each element of `items` in the existing inventory-import endpoint is one item.
Multiline text remains one item; no server-side newline splitting takes place.
Existing limits remain 32,768 UTF-8 bytes per item, at most 10,000 items, and the
endpoint's 2 MiB request-body limit. Imported contents use the existing encrypted
inventory storage and are only returned by an authorized private claim.

The existing `card-key`, `text` and `custom-text` templates keep literal text.
Four additional templates support an explicit versioned JSON envelope:

```json
{"lmm_store_delivery":1,"template":"redemption-code","fields":{"code":"CODE","redeem_url":"https://example.com/redeem","instructions":"Open the redemption page."}}
```

| Template | Required strings | Optional strings |
| --- | --- | --- |
| `redemption-code` | `code` | `redeem_url`, `instructions` |
| `license-key` | `license_key` | `product`, `instructions` |
| `download-link` | `url` | `access_code`, `instructions` |
| `account-details` | `username`, `password` | `url`, `instructions` |

Required values must contain a non-whitespace string. Optional values may be
omitted or empty strings. Null, numbers, arrays, objects and unknown fields are
rejected in a recognized matching v1 envelope; the original strings are never
trimmed or normalized. A partly invalid batch adds no inventory.

Limits apply to UTF-8 bytes: code, license key, password and access code are at
most 4,096; username and product name are at most 1,000; instructions are at most
16,384; URLs are at most 2,048. Nonempty URL fields must be absolute HTTP or HTTPS
URLs with a host, without userinfo or control characters. The server never fetches
or executes a delivery URL. The existing total-item limit still applies.

Unmarked JSON, unknown marker versions, unknown template names and envelopes
whose template differs from the product remain literal text. They are never
silently reinterpreted as another item type. Their original text remains available
for copying, including when it contains multiple lines.

Each new order freezes the product template in `delivery_template`. Order details,
safe pickup metadata and private claim responses return this field. Safe metadata
does not contain delivery fields, passwords or raw inventory contents. Private
claim `items` remain the original strings; the frontend interprets only a valid v1
envelope whose template exactly matches the order's frozen template. Everything
else is displayed and copied as literal text.

The only schema addition for this feature is
`merchant_store_orders.delivery_template VARCHAR(32) NOT NULL DEFAULT ''`, with
no new table or index. Historical orders receive an empty string and always keep
literal delivery; the current product template is never used to infer their format.
Editing a product after purchase does not change a buyer's frozen delivery format.
Independent full-data preservation and previous-version runtime/schema checks
remain release gates. No historical financial migration is replayed.
