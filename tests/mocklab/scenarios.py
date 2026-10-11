"""Acceptance intent and evidence requirements; never substitute provider state for a ledger."""
from __future__ import annotations

from .config import BASELINE

COMMON_INITIAL = "A fresh per-run application database and actual test user/account; no production credentials."
COMMON_CLEANUP = "Stop only this run's processes; remove only its disposable databases/directories after evidence is saved."

# A production driver is intentionally NOT invented while the WIP host and funds
# bridge are incomplete. These specifications are part of the acceptance report,
# not placeholder functions that return success.
SPECS = [
    ("payment.once", "payments", "POST a real application order for 1000 USD minor units.",
     ["Provider Checkout succeeds", "Deliver a valid paid event", "Run the real payment worker"],
     "Application confirms credit only after a real Rust receipt.",
     "One order and one credit journal; journal/account/quote units match; no duplicate balance delta."),
    ("payment.callback-order", "payments", "Use the same real order and frozen quote.",
     ["Capture an earlier failure", "Pay", "Deliver paid twice", "Deliver the earlier failure last"],
     "Duplicate events are accepted safely; old failure cannot reverse terminal success.",
     "One credited order, one credit journal, durable event evidence; no second credit."),
    ("payment.timeout-query", "payments", "Create a real application order with a stable request key.",
     ["Provider commits Checkout but withholds response", "Application times out", "Recover the same provider object", "Query", "Deliver paid evidence"],
     "Unknown outcome stays pending; query observations are not silently converted into signed proof.",
     "One provider order; one eventual credit only after independently accepted evidence and Rust receipt."),
    ("payment.partial-refund", "payments", "A credited 1000-minor-unit order; request a 400-minor-unit refund.",
     ["Real Rust refund hold", "Provider refund pending", "Signed refund success", "Duplicate success", "Request another 600 then reject excess"],
     "Pending until verified refund evidence and a matching committed receipt.",
     "400 then 600 refunded at the original conversion quote; cumulative cap 1000; no double release or debit."),
    ("model.normal", "models", "Send a normal request through the actual model entry, with an authorized paid test key.",
     ["Real channel selected", "Provider returns bounded normal response with usage", "Actual core settles"],
     "Actual adapter parses and returns protocol-correct content.",
     "One usage entry and one settlement; correct payer, price version, budget and remaining reservation."),
    ("model.stream", "models", "Send a streaming model request through the actual entry.",
     ["Start", "Deterministic protocol-specific deltas", "Protocol-specific finish and final usage"],
     "Correct ordered events, no extra protocol terminator, one final result.",
     "One usage/settlement; cumulative usage not summed per chunk; no active stream or held connection."),
    ("model.cancel", "models", "Send a streamed request; cancel after the first content event.",
     ["Reserve", "Receive first content", "Client disconnects", "Cancellation reaches provider", "Reconcile actual consumed usage"],
     "No abandoned stream; cancellation does not invent zero upstream consumption.",
     "No duplicate charge; bounded unresolved hold or actual reconciliation according to the real core contract; resources released."),
    ("model.missing-usage", "models", "Send a request to an explicitly configured missing-usage fixture.",
     ["Provider responds without usage", "Actual adapter handles incomplete evidence"],
     "No invented usage or unconditionally free call; documented fallback or reconciliation state.",
     "The real policy determines the usage/hold result; test must assert it explicitly, not accept any balance."),
    ("model.upstream-failure", "models", "Send a paid request to a failing upstream instance.",
     ["Reserve", "Provider returns declared 429/503", "Actual retry policy runs or rejects"],
     "Bounded error and attempts; no success fabricated by the runner.",
     "No successful-use journal for an unaccepted call; actual failure/hold policy and release are checked."),
    ("channel.failover", "channels", "Create real primary and backup channels with the actual group/model/priority configuration.",
     ["Primary returns 503", "Real router chooses permitted backup", "Backup responds"],
     "Actual channel selection and attempt trace agree with routing policy.",
     "One completed request/settlement; correct model mapping, payer and budget; no charge per retry."),
    ("channel.recovery", "channels", "Keep the same channels; use the actual breaker/health state.",
     ["Primary unavailable", "Advance provider clock and real application clock hook independently", "Primary returns healthy", "Actual router probes/recovers"],
     "Recovery follows the real router policy, not a test-written fallback loop.",
     "Correct route trace and bounded attempts; no duplicate usage or permanent resource loss."),
    ("tool.quote-success", "tools", "Register/discover/publish a MOCK ONLY tool through the real market; fix its version and USD-millionth price.",
     ["Real registration and MCP discovery", "Load immutable quote", "Rust reserve", "mock_add(2,3)", "Persist result", "Rust commit"],
     "Result value is 5; displayed rule, quote and execution share the same immutable price version.",
     "One market execution, one provider call, one real funds settlement and matching usage record."),
    ("tool.failure-billing", "tools", "Use a real published fixed-per-invocation mock_fail tool.",
     ["Load pinned quote", "Real reserve", "Provider returns isError=true", "Real persistence and settlement", "Retry same execution key"],
     "isError remains a provider result; an accepted invocation is billable under this repository's fixed-price contract.",
     "One provider invocation and one settlement at the pinned price; replay reads saved result, not another provider call."),
    ("tool.transport-uncertain", "tools", "Execute a published disconnect fixture through the real market.",
     ["Reserve", "Provider accepts", "Transport disconnects before complete result", "Reconcile the uncertain execution"],
     "No automatic re-execution or release merely because the response was lost.",
     "Durable execution_in_doubt plus reservation/reconciliation evidence; no fabricated success or balance mutation."),
]


def requirements() -> list[dict]:
    return [{"scenario_id": ident, "domain": domain, "initial_state": COMMON_INITIAL, "input": input_,
             "mock_events": events, "expected_response": response, "expected_database": database,
             "cleanup": COMMON_CLEANUP,
             "reviewed_baseline": BASELINE} for ident, domain, input_, events, response, database in SPECS]
