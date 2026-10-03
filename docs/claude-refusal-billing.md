# Claude refusals without output

The Go backend provides an optional zero-charge policy for Claude refusals without
output. The policy is disabled by default.

In **System Settings → Models → Claude**, enable **Do Not Charge for Claude
Refusals Without Output** to opt in. The saved boolean option is
`claude.refusal_no_output_no_charge_enabled` (`false` by default). The settings
loader also uses `false` when the server has no value for this option.

Enable this policy only when every Claude provider selected by the deployment
follows Anthropic's no-charge behavior for these responses. A third-party
provider may still bill the deployment for a refused request; enabling this
setting does not change the provider's invoice. Leave it disabled for mixed or
unverified providers.

The policy requires an explicit Claude refusal, no output content blocks, and
upstream usage that explicitly reports zero output tokens. Missing usage is not
proof of zero output. Streaming requests require this count in the final
`message_delta`; an initial zero in `message_start` is not sufficient. A refusal
after output is produced does not qualify, and an empty response by itself is
not a refusal. Qualifying requests consume no user
quota; disabling the policy keeps the normal billing rules.

As of September 2026, Anthropic bills zero-output refusals when
`stop_details.category` is `bio`, `frontier_llm`, or `reasoning_extraction`.
Those categories continue to settle normally even when this setting is enabled;
other categories and a null/absent category can qualify. See
[Anthropic's refusal billing rules](https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback#how-refusals-are-billed).
Requests with non-empty `usage.iterations` also keep normal settlement: the
top-level zero-output attempt does not prove that earlier fallback attempts
were free. Malformed refusal categories are treated conservatively.

Settlement refunds the entire prepayment, including wallet, subscription, and
API-key quota. Ratio pricing, fixed prices, tiered expressions, and tool
surcharges are skipped for the qualifying request. Request counts still increase;
quota and token consumption aggregates remain zero. Consume logs keep the
measured upstream input/cache tokens for diagnosis and expose
`billing_exempt_reason=claude_refusal_no_output`; the refusal debug reason stays
inside the admin-only log metadata. Usage returned to the client is unchanged.

Both streaming and non-streaming requests use the same upstream evidence check,
including native Messages and conversions to Chat Completions or Responses.
The Claude adaptor now connects the existing Responses request/response
converters; this path previously returned an unsupported conversion error.
Streaming content blocks, even empty text or tool blocks, prevent the exemption.
Client identity, group ratios, model price locks, `/fast`, OAuth mode, and admin
AI state do not decide whether the policy applies.
