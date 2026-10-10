# Support module (task 07)

Owns its database, customer notes/tags, conversations, message sequence and replay
records. It depends on a narrow `Directory` (shop owner and order parties), a Rust
`Authority`, and its own `Repository`. It never opens a store or core database.
`store.Service` supplies the read-only directory port; shared account/HTTP types
do not give support access to the store repository or money port.

Apply `schema.sql` only to a new extension database. Inject `NewPostgres(pool)`
and `New(repo, auth, commerce)`, then register in the final integration task.
Nothing in this module changes the public host registration or protobuf files.

## API under /extensions/v1/support

- `POST /conversations`: `shop_id`, `buyer`, optional `order_id`, `subject`, `key`.
  Buyers may ask pre-sale questions. Sellers need a verified order relationship
  when initiating contact. An order must belong to both the supplied shop and buyer.
- `POST /conversations/change`: `shop_id`, `conversation_id`, `key`, `action`, `body`.
  Actions are `message`, `close`, `reopen`. Message bodies are bounded to 8192 bytes.
- `GET /shops/{shop}/conversations/{conversation}`: bounded message page with
  `after`/`limit`. The cursor is confined to that conversation.
- `POST /customers/change`: `shop_id`, `customer_id`, `key`, `notes`, `tags`.
  Seller management only; notes never appear in the buyer's conversation response.
- `GET /shops/{shop}/manage/{customers|conversations}`: seller-only bounded lists.

Every private operation rechecks Rust authority. Team membership grants buyer
read access; seller replies and private customer edits require team owner/admin.
An L5/L6 platform role does not grant access to another shop. Authors are always
resolved from the verified credential, not accepted from JSON.

A customer is created on the first verified conversation. The idempotent
`CustomerFromOrder(ctx, shopID, orderID)` in-process port also builds customer
records from order events. Final integration should call this from its approved
order event consumer; there is no hidden worker or assumed event subscription.
The port re-reads the order parties through Directory, rejects invalid links, and
never overwrites existing private notes or tags. Event payload buyer IDs are not
trusted. Existing conversations can work while the store is unavailable, provided
Rust still verifies user authority. New contacts fail closed when the directory
cannot verify their relationship.

Messages and conversation sequence commit together. Reusing a message key with
the same body returns the original result; a changed payload conflicts. Seller
notes use a separate record and replay namespace. All lookups include shop scope;
message queries also include the conversation prefix. No credentials are stored.

Tests: run the commands in `../store/README.md`. The PostgreSQL test runner also
exercises this module with a separate injected database pool.
