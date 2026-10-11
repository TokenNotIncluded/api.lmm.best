"""Bounded probes of a running mock process. These are NOT application E2E tests."""
from __future__ import annotations

import base64
import hashlib
import hmac
import http.client
import time
from urllib.parse import urlencode

from .client import Client
from .config import Invalid, loads
from .mcp import VERSION
from .payments import EPAY_MERCHANT, EPAY_SECRET, STRIPE_SECRET, epay_sign
from .report import row
from .test_mocklab import STRIPE_HEADERS, checkout_form, events


def running_probes(client: Client) -> list[dict]:
    results = []
    def check(name, function):
        started = time.monotonic()
        try:
            evidence = function()
            results.append(row("running-provider." + name, "mock-component", "PASS", started, **(evidence or {})))
        except (AssertionError, Invalid, KeyError, IndexError, TypeError, OSError, http.client.HTTPException) as exc:
            results.append(row("running-provider." + name, "mock-component", "FAIL", started,
                               reason=type(exc).__name__ + ": " + str(exc)[:240]))
    for name, item in client.manifest["instances"].items():
        # Only normal instances are smoke-probed. The executable unit scenarios
        # cover fault controls. This loop never chooses a fallback for the app.
        fixture = item.get("fixture", {})
        scenario = fixture.get("scenario", {})
        if any(scenario.get(k, 0) for k in ("first_failures", "recover_at_ms", "first_byte_ms", "chunk_delay_ms")) or scenario.get("kind", "normal") != "normal":
            continue
        p = item["protocol"]
        model = item.get("model", "mock-small")
        if p in {"openai-chat", "openai-responses", "anthropic-messages", "gemini-generate-content"}:
            def probe(name=name, p=p, model=model):
                path, request = {"openai-chat": ("/v1/chat/completions", {"model":model, "messages":[]}),
                                 "openai-responses": ("/v1/responses", {"model":model, "input":"test"}),
                                 "anthropic-messages": ("/v1/messages", {"model":model, "messages":[], "max_tokens":32}),
                                 "gemini-generate-content": (f"/v1beta/models/{model}:generateContent", {"contents":[]})}[p]
                status, _, raw = client.request(name,"POST",path,request)
                if status != 200:
                    raise AssertionError(f"normal provider response was {status}")
                normal = loads(raw)
                if p == "gemini-generate-content":
                    path = f"/v1beta/models/{model}:streamGenerateContent?alt=sse"
                else:
                    request["stream"] = True
                    if p == "openai-chat":
                        request["stream_options"] = {"include_usage":True}
                status, _, raw = client.request(name,"POST",path,request)
                if status != 200:
                    raise AssertionError(f"stream response was {status}")
                frames = events(raw)
                if p == "openai-chat":
                    assert normal["object"] == "chat.completion" and frames[-1] == "[DONE]"
                    assert frames[-2]["usage"]["total_tokens"] == normal["usage"]["total_tokens"]
                elif p == "openai-responses":
                    assert normal["object"] == "response" and frames[-1]["type"] == "response.completed"
                    assert [f["sequence_number"] for f in frames] == list(range(len(frames)))
                elif p == "anthropic-messages":
                    assert normal["type"] == "message" and frames[-1]["type"] == "message_stop"
                    assert frames[-2]["usage"]["output_tokens"] == normal["usage"]["output_tokens"]
                else:
                    assert normal["candidates"] and frames[-1]["candidates"][0]["finishReason"] == "STOP"
                    assert frames[-1]["usageMetadata"] == normal["usageMetadata"]
                return {"protocol":p,"normal_and_stream":True,"frames":len(frames)}
            check(name,probe)
        elif p == "stripe-v1":
            def probe(name=name):
                order = "mock_probe_" + client.manifest["run_id"]
                headers = {**STRIPE_HEADERS,"Idempotency-Key":order}
                form = checkout_form(currency=client.manifest["instances"][name].get("fixture",{}).get("currency","USD").lower(), order=order)
                status,_,raw = client.request(name,"POST","/v1/checkout/sessions",form=form,headers=headers)
                assert status == 200
                session = loads(raw)
                assert client.request(name,"POST","/v1/checkout/sessions",form=form,headers=headers)[2] == raw
                event = client.control("transition",instance=name,reference=session["payment_intent"],state="succeeded")["event"]
                delivery = client.control("delivery",instance=name,event=event)
                raw = base64.b64decode(delivery["body_base64"],validate=True)
                stamp, sig = delivery["headers"]["Stripe-Signature"].split(",v1=")
                expected = hmac.new(STRIPE_SECRET.encode(),stamp.removeprefix("t=").encode()+b"."+raw,hashlib.sha256).hexdigest()
                assert hmac.compare_digest(sig,expected)
                assert loads(raw)["data"]["object"]["metadata"]["lmm_order_id"] == order
                return {"provider_order":session["id"],"event":event,"signature_checked":True,
                        "ledger_checked":False}
            check(name,probe)
        elif p == "epay-md5-cny":
            def probe(name=name):
                order="epay_probe_"+client.manifest["run_id"]
                values={"pid":EPAY_MERCHANT,"out_trade_no":order,"type":"alipay","money":"10.01","sign_type":"MD5"}
                values["sign"]=epay_sign(values)
                assert client.request(name,"GET","/submit.php?"+urlencode(values))[0] == 200
                event=client.control("transition",instance=name,reference=order,state="succeeded")["event"]
                status,_,raw=client.request(name,"GET","/api.php?"+urlencode({"act":"order","pid":EPAY_MERCHANT,"key":EPAY_SECRET,"out_trade_no":order}))
                assert status==200 and loads(raw)["money"]=="10.01" and loads(raw)["status"]==1
                return {"provider_order":order,"event":event,"ledger_checked":False}
            check(name,probe)
        elif p == "mcp-2025-11-25":
            def probe(name=name):
                headers={"Mcp-Protocol-Version":VERSION}
                def rpc(method,params,ident=1):
                    req={"jsonrpc":"2.0","method":method,"params":params}
                    if ident is not None:
                        req["id"]=ident
                    return client.request(name,"POST","/mcp",req,headers=headers)
                status, received, raw=rpc("initialize",{"protocolVersion":VERSION,"capabilities":{},"clientInfo":{"name":"mocklab-probe","version":"1"}})
                assert status==200 and loads(raw)["result"]["protocolVersion"]==VERSION
                headers["Mcp-Session-Id"]=received["Mcp-Session-Id"]
                try:
                    assert rpc("notifications/initialized",{},None)[0]==202
                    assert len(loads(rpc("tools/list",{})[2])["result"]["tools"])==5
                    result=loads(rpc("tools/call",{"name":"mock_add","arguments":{"a":2,"b":3}})[2])["result"]
                    assert not result["isError"] and loads(result["content"][0]["text"])["value"]==5
                    assert loads(rpc("tools/call",{"name":"mock_fail","arguments":{}})[2])["result"]["isError"]
                finally:
                    assert client.request(name,"DELETE","/mcp",headers=headers)[0]==204
                return {"protocol":VERSION,"market_registration_checked":False,"ledger_checked":False}
            check(name,probe)
    return results
