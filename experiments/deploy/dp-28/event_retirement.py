"""DP-25 handoff: eligibility assessment ONLY; never opens or edits a database."""
from __future__ import annotations
from typing import Any

REQUIRED_PROOFS = (
    "effect_final_and_durable",
    "every_required_consumer_ack_durable",
    "no_pending_recovery_or_dispute",
    "replay_frontier_past_event",
    "restore_and_replay_window_covered",
    "dedup_uniqueness_evidence_retained",
    "financial_audit_and_ledger_links_retained",
    "payload_retention_deadline_reached",
)


def assess(proofs: dict[str, Any]) -> dict[str, Any]:
    missing = [name for name in REQUIRED_PROOFS if proofs.get(name) is not True]
    return {
        "payload_candidate_only": not missing,
        "blocking_proofs": missing,
        "execution": "not-implemented--database-owner-transaction-required",
        "never_remove": ["dedup-uniqueness", "event-id", "effect-id", "request-hash", "ledger-links"],
    }
