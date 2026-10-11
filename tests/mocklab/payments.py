"""Provider-side orders only. There are no wallet, budget or journal mutations here."""
from __future__ import annotations

import base64
import copy
import hashlib
import hmac
import re
from urllib.parse import urlencode

from .config import Instance, Invalid, integer, wire
from .protocol import Reply, json_reply, ProviderError

STRIPE_KEY = "sk_test_mocklab_only"
STRIPE_SECRET = "whsec_mocklab_only_not_a_real_secret"
STRIPE_VERSION = "mocklab-v1-fixture"
STRIPE_MERCHANT = "acct_mocklab"
EPAY_MERCHANT = "1001"
EPAY_SECRET = "mocklab_epay_secret_only"
MAX_MINOR = 9_000_000_000_000


def required(form: dict, key: str) -> str:
    value = form.get(key)
    if not isinstance(value, str) or not value or len(value) > 2048:
        raise Invalid("missing provider field")
    return value


def minor(value: str) -> int:
    if not isinstance(value, str) or not re.fullmatch(r"[0-9]{1,13}", value):
        raise Invalid("amount must be integer minor units")
    return integer(int(value), 1, MAX_MINOR)


def epay_sign(values: dict[str, str]) -> str:
    body = "&".join(f"{k}={v}" for k, v in sorted(values.items()) if k not in {"sign", "sign_type"} and v)
    return hashlib.md5((body + EPAY_SECRET).encode(), usedforsecurity=False).hexdigest()


def decimal_minor(value: str) -> int:
    if not isinstance(value, str) or not re.fullmatch(r"[0-9]{1,11}(\.[0-9]{1,2})?", value):
        raise Invalid("ePay requires exact CNY decimal units")
    whole, _, fraction = value.partition(".")
    return integer(int(whole) * 100 + int((fraction + "00")[:2]), 1, MAX_MINOR)


class Payments:
    def __init__(self, cfg: Instance, uid):
        self.cfg, self.uid = cfg, uid
        self.sessions: dict[str, dict] = {}
        self.payments: dict[str, dict] = {}
        self.refunds: dict[str, dict] = {}
        self.events: dict[str, dict] = {}
        self.keys: dict[str, tuple[bytes, dict]] = {}
        self.epay_orders: dict[str, dict] = {}

    def _event(self, kind: str, obj: dict, now: int) -> str:
        ident = self.uid("evt")
        self.events[ident] = {"id": ident, "object": "event", "type": kind,
                              "livemode": False, "created": now, "data": {"object": copy.deepcopy(obj)}}
        return ident

    def _idempotent(self, headers: dict, method: str, path: str, form: dict, action) -> dict:
        key = headers.get("idempotency-key", "")
        if not re.fullmatch(r"[A-Za-z0-9_.-]{16,128}", key):
            raise Invalid("a bounded test Idempotency-Key is required")
        digest = hashlib.sha256(wire([method, path, form])).digest()
        if key in self.keys:
            old, response = self.keys[key]
            if not hmac.compare_digest(old, digest):
                raise ProviderError(409, "idempotency_conflict")
            return copy.deepcopy(response)
        response = action()
        self.keys[key] = (digest, copy.deepcopy(response))
        return response

    def stripe(self, method: str, path: str, query: dict, form: dict, headers: dict, now: int) -> Reply:
        if headers.get("authorization") != "Bearer " + STRIPE_KEY or headers.get("stripe-version") != STRIPE_VERSION:
            raise ProviderError(401, "mock_test_credentials_required")
        if method == "GET":
            if path == "/v1/account":
                return json_reply({"id": STRIPE_MERCHANT, "object": "account"})
            for prefix, table in (("/v1/events/", self.events), ("/v1/payment_intents/", self.payments),
                                  ("/v1/checkout/sessions/", self.sessions), ("/v1/refunds/", self.refunds)):
                if path.startswith(prefix) and path[len(prefix):] in table:
                    return json_reply(table[path[len(prefix):]])
            if path == "/v1/refunds":
                payment = required(query, "payment_intent")
                if payment not in self.payments:
                    raise ProviderError(404, "payment_not_found")
                data = [r for r in self.refunds.values() if r["payment_intent"] == payment]
                if query.get("starting_after"):
                    ids = [r["id"] for r in data]
                    if query["starting_after"] not in ids:
                        raise ProviderError(400, "invalid_cursor")
                    data = data[ids.index(query["starting_after"]) + 1:]
                try:
                    limit = integer(int(query.get("limit", "100")), 1, 100)
                except ValueError as exc:
                    raise Invalid("invalid page size") from exc
                return json_reply({"object": "list", "data": data[:limit], "has_more": len(data) > limit})
            raise ProviderError(404, "provider_object_not_found")
        if method != "POST":
            raise ProviderError(405, "method_not_supported")
        if path == "/v1/checkout/sessions":
            def create():
                if form.get("mode") != "payment" or form.get("line_items[0][quantity]") != "1":
                    raise Invalid("only fixed one-time Checkout is implemented")
                order = required(form, "client_reference_id")
                if (form.get("metadata[lmm_order_id]") != order or
                        form.get("payment_intent_data[metadata][lmm_order_id]") != order):
                    raise Invalid("checkout order binding mismatch")
                currency = required(form, "line_items[0][price_data][currency]")
                if currency.upper() != self.cfg.currency:
                    raise Invalid("currency mismatch")
                amount = minor(required(form, "line_items[0][price_data][unit_amount]"))
                payment, session = self.uid("pi"), self.uid("cs")
                self.payments[payment] = {"id": payment, "object": "payment_intent", "status": "requires_payment_method",
                                          "amount": amount, "amount_received": 0, "currency": currency.lower(),
                                          "livemode": False, "metadata": {"lmm_order_id": order}}
                self.sessions[session] = {"id": session, "object": "checkout.session", "mode": "payment", "status": "open",
                                          "payment_status": "unpaid", "amount_total": amount, "currency": currency.lower(),
                                          "livemode": False, "payment_intent": payment, "client_reference_id": order,
                                          "metadata": {"lmm_order_id": order},
                                          "url": "https://checkout.stripe.com/c/pay/" + session}
                return self.sessions[session]
            return json_reply(self._idempotent(headers, method, path, form, create))
        if path == "/v1/refunds":
            def refund():
                payment = required(form, "payment_intent")
                p = self.payments.get(payment)
                if not p or p["status"] != "succeeded":
                    raise ProviderError(409, "payment_not_paid")
                amount = minor(required(form, "amount"))
                held = sum(r["amount"] for r in self.refunds.values()
                           if r["payment_intent"] == payment and r["status"] not in {"failed", "canceled"})
                if held + amount > p["amount"]:
                    raise ProviderError(409, "refund_exceeds_payment")
                order = form.get("metadata[lmm_order_id]")
                if order != p["metadata"]["lmm_order_id"]:
                    raise Invalid("refund order mismatch")
                ident = self.uid("re")
                r = {"id": ident, "object": "refund", "status": "pending", "amount": amount,
                     "currency": p["currency"], "payment_intent": payment, "livemode": False,
                     "metadata": {"lmm_order_id": order, "lmm_refund_id": required(form, "metadata[lmm_refund_id]")}}
                self.refunds[ident] = r
                self._event("refund.created", r, now)
                return r
            return json_reply(self._idempotent(headers, method, path, form, refund))
        if path.startswith("/v1/checkout/sessions/") and path.endswith("/expire"):
            ident = path.removeprefix("/v1/checkout/sessions/").removesuffix("/expire")
            session = self.sessions.get(ident)
            if not session or session["status"] != "open":
                raise ProviderError(409, "session_not_open")
            session["status"] = "expired"
            return json_reply(session)
        raise ProviderError(404, "operation_not_implemented")

    def epay(self, method: str, path: str, query: dict, form: dict, headers: dict, now: int) -> Reply:
        if path == "/submit.php" and method in {"GET", "POST"}:
            values = query if method == "GET" else form
            if (values.get("pid") != EPAY_MERCHANT or values.get("sign_type") != "MD5"
                    or not hmac.compare_digest(values.get("sign", ""), epay_sign(values))):
                raise ProviderError(401, "invalid_mock_epay_signature")
            if values.get("type") != "alipay":
                raise Invalid("fixture payment type is alipay")
            order = required(values, "out_trade_no")
            amount = decimal_minor(required(values, "money"))
            if order in self.epay_orders:
                old = self.epay_orders[order]
                if old["amount_minor"] != amount:
                    raise ProviderError(409, "order_amount_conflict")
            else:
                self.epay_orders[order] = {"code": 1, "pid": EPAY_MERCHANT, "trade_no": self.uid("trade"),
                    "out_trade_no": order, "type": "alipay", "money": f"{amount // 100}.{amount % 100:02d}",
                    "amount_minor": amount, "status": 0}
            return Reply(200, b"<!doctype html><title>MOCK ONLY</title><p>Test payment pending.</p>",
                         {"Content-Type": "text/html; charset=utf-8"})
        if path == "/api.php" and method == "GET" and query.get("act") == "order":
            if query.get("pid") != EPAY_MERCHANT or query.get("key") != EPAY_SECRET:
                raise ProviderError(401, "mock_test_credentials_required")
            order = self.epay_orders.get(query.get("out_trade_no"))
            return json_reply({k: v for k, v in order.items() if k != "amount_minor"} if order else {"code": -1})
        if query.get("act") == "refund":
            raise ProviderError(501, "legacy_epay_refund_proof_not_supported")
        raise ProviderError(404, "operation_not_implemented")

    def transition(self, reference: str, state: str, now: int) -> str:
        if self.cfg.protocol == "epay-md5-cny":
            obj = self.epay_orders.get(reference)
            if obj is None or state != "succeeded":
                raise Invalid("unsupported ePay transition")
            obj["status"] = 1
            event = {"pid": EPAY_MERCHANT, "type": "alipay", "out_trade_no": reference,
                     "trade_no": obj["trade_no"], "money": obj["money"], "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
            event["sign"] = epay_sign(event)
            ident = self.uid("evt")
            self.events[ident] = event
            return ident
        if reference in self.refunds:
            obj = self.refunds[reference]
            if obj["status"] in {"succeeded", "failed", "canceled"} and obj["status"] != state:
                raise Invalid("cannot change a terminal refund")
            if state not in {"succeeded", "failed", "canceled", "pending"}:
                raise Invalid("unsupported refund state")
            obj["status"] = state
            return self._event("refund.failed" if state in {"failed", "canceled"} else "refund.updated", obj, now)
        obj = self.payments.get(reference)
        if obj is None or state not in {"succeeded", "failed"}:
            raise Invalid("unknown payment or unsupported state")
        if obj["status"] == "succeeded" and state == "failed":
            raise Invalid("capture an earlier failure event and deliver it late instead")
        if any(s["status"] == "expired" for s in self.sessions.values() if s["payment_intent"] == reference):
            raise Invalid("expired checkout cannot be paid")
        obj["status"] = "succeeded" if state == "succeeded" else "requires_payment_method"
        obj["amount_received"] = obj["amount"] if state == "succeeded" else 0
        if state == "succeeded":
            for s in self.sessions.values():
                if s["payment_intent"] == reference:
                    s.update(status="complete", payment_status="paid")
        return self._event("payment_intent.succeeded" if state == "succeeded" else "payment_intent.payment_failed", obj, now)

    def delivery(self, event_id: str, now: int) -> dict:
        event = self.events.get(event_id)
        if event is None:
            raise Invalid("unknown provider event")
        if self.cfg.protocol == "epay-md5-cny":
            body = urlencode(event).encode()
            headers = {"Content-Type": "application/x-www-form-urlencoded"}
        else:
            body = wire(event)
            signature = hmac.new(STRIPE_SECRET.encode(), str(now).encode() + b"." + body, hashlib.sha256).hexdigest()
            headers = {"Content-Type": "application/json", "Stripe-Signature": f"t={now},v1={signature}"}
        return {"event_id": event_id, "method": "POST", "body_base64": base64.b64encode(body).decode(), "headers": headers}

    def count(self) -> int:
        return sum(map(len, (self.sessions, self.payments, self.refunds, self.events, self.keys, self.epay_orders)))
