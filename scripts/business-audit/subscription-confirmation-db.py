# Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
"""Execute the production confirmation SELECT against an isolated SQLite fixture.

This verifies query semantics and a simulated settlement, NOT provider callbacks
or the Go HTTP/GORM stack. No network, application config or existing DB is used.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import re
import sqlite3
import tempfile
from threading import Barrier

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "apps/api-go/model/subscription_checkout_confirmation.go"
SQL = re.search(r"const subscriptionCheckoutConfirmationSQL = `([^`]+)`", SOURCE.read_text()).group(1)
SCHEMA = """
CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, quota INTEGER);
CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY, title TEXT, total_amount INTEGER);
CREATE TABLE user_subscriptions (
 id INTEGER PRIMARY KEY, user_id INTEGER, plan_id INTEGER, amount_total INTEGER,
 amount_used INTEGER DEFAULT 0, status TEXT, start_time INTEGER, end_time INTEGER,
 next_reset_time INTEGER DEFAULT 0);
CREATE TABLE subscription_orders (
 id INTEGER PRIMARY KEY, user_id INTEGER, plan_id INTEGER, trade_no TEXT UNIQUE,
 status TEXT, complete_time INTEGER DEFAULT 0, user_subscription_id INTEGER DEFAULT 0,
 money REAL, expected_amount_micros INTEGER, settlement_currency TEXT);
CREATE TABLE subscription_payment_events (
 id INTEGER PRIMARY KEY, subscription_order_id INTEGER, provider_transaction_id TEXT UNIQUE,
 settlement_amount_micros INTEGER, settlement_currency TEXT);
"""


def connect(path: Path) -> sqlite3.Connection:
    db = sqlite3.connect(path, timeout=5)
    db.row_factory = sqlite3.Row
    return db


def query(db: sqlite3.Connection, trade: str, user: int = 1) -> dict | None:
    row = db.execute(SQL, ("success", user, trade)).fetchone()
    if row is None:
        return None
    result = dict(row)
    result["confirmed"] = bool(result["confirmed"])
    return result


def settle_fixture(db: sqlite3.Connection, order: int, grant: int) -> None:
    """External payment stub: a synthetic receipt and grant in one transaction."""
    with db:
        row = db.execute("SELECT * FROM subscription_orders WHERE id=?", (order,)).fetchone()
        db.execute("INSERT INTO subscription_payment_events VALUES (?,?,?,?,?)",
                   (order, order, f"simulated-payment-{order}", row["expected_amount_micros"], "USD"))
        db.execute("INSERT INTO user_subscriptions VALUES (?,?,?,?,?,?,?,?,?)",
                   (grant, row["user_id"], row["plan_id"], 1000, 0, "active", 200, 1000000, 10000))
        db.execute("UPDATE subscription_orders SET status='success',complete_time=200,user_subscription_id=? WHERE id=?", (grant, order))


def run(path: Path) -> dict:
    # Never open an existing database supplied by the caller.
    with path.open("x"):
        pass
    db = connect(path)
    db.execute("PRAGMA journal_mode=WAL")
    db.executescript(SCHEMA)
    with db:
        db.executemany("INSERT INTO users VALUES (?,?,?)", [(1, "fixture-owner", 5000), (2, "fixture-other", 5000)])
        db.executemany("INSERT INTO subscription_plans VALUES (?,?,?)", [(3, "fixture-plan", 1000), (4, "other-plan", 1000)])
        db.executemany("INSERT INTO user_subscriptions VALUES (?,?,?,?,?,?,?,?,?)", [
            (42, 1, 3, 1000, 10, "active", 100, 1000000, 10000),
            (90, 2, 3, 1000, 0, "active", 100, 1000000, 10000),
            (91, 1, 4, 1000, 0, "active", 100, 1000000, 10000),
        ])
        db.executemany("INSERT INTO subscription_orders VALUES (?,?,?,?,?,?,?,?,?,?)", [
            (1, 1, 3, "order-A", "pending", 0, 0, 10, 10000000, "USD"),
            (2, 1, 3, "order-B", "pending", 0, 0, 10, 10000000, "USD"),
            (3, 2, 3, "other-user", "pending", 0, 0, 10, 10000000, "USD"),
            (4, 1, 3, "bad-link", "success", 200, 90, 10, 10000000, "USD"),
            (5, 1, 3, "order-E", "pending", 0, 0, 10, 10000000, "USD"),
            (6, 1, 3, "order-F", "pending", 0, 0, 10, 10000000, "USD"),
        ])
    timeline = {"pending": query(db, "order-A")}
    for name, statement in [
        ("old_consumption", "UPDATE user_subscriptions SET amount_used=11 WHERE id=42"),
        ("old_reset", "UPDATE user_subscriptions SET amount_used=0,next_reset_time=20000 WHERE id=42"),
        ("old_expiry", "UPDATE user_subscriptions SET status='expired',end_time=150 WHERE id=42"),
    ]:
        with db:
            db.execute(statement)
        timeline[name] = query(db, "order-A")
        assert timeline[name] == timeline["pending"]
    settle_fixture(db, 2, 44)
    timeline["other_device_paid"] = query(db, "order-B")
    timeline["a_after_b_paid"] = query(db, "order-A")
    assert timeline["a_after_b_paid"]["confirmed"] is False
    assert timeline["other_device_paid"]["confirmed"] is True
    assert query(db, "other-user") is None
    assert query(db, "missing") is None
    assert query(db, "order-A' OR 1=1 --") is None
    assert query(db, "bad-link")["user_subscription_id"] == 0
    with db:
        db.execute("UPDATE subscription_orders SET user_subscription_id=91 WHERE id=4")
    assert query(db, "bad-link")["confirmed"] is False
    with db:
        db.execute("UPDATE subscription_orders SET user_subscription_id=999 WHERE id=4")
    assert query(db, "bad-link")["confirmed"] is False
    for status in ["cancelled", "expired", "failed"]:
        with db:
            db.execute("UPDATE subscription_orders SET status=? WHERE id=1", (status,))
        timeline[status] = query(db, "order-A")
        assert timeline[status]["confirmed"] is False
    with db:
        db.execute("UPDATE subscription_orders SET status='success',complete_time=200 WHERE id=1")
        db.execute("INSERT INTO subscription_payment_events VALUES (1,1,'simulated-payment-1',10000000,'USD')")
    timeline["paid_without_grant"] = query(db, "order-A")
    assert timeline["paid_without_grant"]["confirmed"] is False
    # An interrupted grant rolls back and cannot be observed as confirmed.
    try:
        with db:
            db.execute("INSERT INTO user_subscriptions VALUES (43,1,3,1000,0,'active',200,1000000,10000)")
            db.execute("UPDATE subscription_orders SET user_subscription_id=43 WHERE id=1")
            raise RuntimeError("simulated interruption before commit")
    except RuntimeError:
        pass
    assert query(db, "order-A")["confirmed"] is False
    observer = connect(path)
    with db:
        db.execute("INSERT INTO user_subscriptions VALUES (43,1,3,1000,0,'active',200,1000000,10000)")
        db.execute("UPDATE subscription_orders SET user_subscription_id=43 WHERE id=1")
        assert query(observer, "order-A")["confirmed"] is False
    timeline["late_own_grant"] = query(observer, "order-A")
    assert timeline["late_own_grant"]["confirmed"] is True
    observer.close()
    # Two separate connections compete to settle two different orders.
    barrier = Barrier(2)
    def purchase(pair: tuple[int, int]) -> None:
        client = connect(path)
        try:
            barrier.wait(timeout=5)
            settle_fixture(client, *pair)
        finally:
            client.close()
    with ThreadPoolExecutor(max_workers=2) as pool:
        list(pool.map(purchase, [(5, 45), (6, 46)]))
    assert query(db, "order-E")["user_subscription_id"] == 45
    assert query(db, "order-F")["user_subscription_id"] == 46
    def snapshot() -> dict:
        return {table: [dict(row) for row in db.execute(f"SELECT * FROM {table} ORDER BY id")]
                for table in ["users", "subscription_plans", "subscription_orders", "subscription_payment_events", "user_subscriptions"]}
    before = snapshot()
    for _ in range(3):
        assert query(db, "order-A") == timeline["late_own_grant"]
    assert snapshot() == before, "confirmation reads must not write payments, balance, orders, or grants"
    # Missing tables/read failures must be errors, not successful empty evidence.
    db.execute("BEGIN")
    db.execute("DROP TABLE user_subscriptions")
    try:
        query(db, "order-A")
        raise AssertionError("read failure was swallowed")
    except sqlite3.OperationalError:
        pass
    db.rollback()
    db.close()
    return {"scope": "production SELECT + synthetic settlement; not Go HTTP/GORM or provider callbacks",
            "timeline": timeline, "records": before,
            "checks": ["old use/reset/expiry", "other order/device", "owner and plan isolation",
                       "missing grant", "cancel/expire/fail", "rollback", "uncommitted visibility",
                       "two concurrent fixture settlements", "duplicate read is read-only", "read failure"]}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, help="new local fixture file only; must not exist")
    args = parser.parse_args()
    if args.database:
        result = run(args.database)
    else:
        with tempfile.TemporaryDirectory(prefix="bi03-") as directory:
            result = run(Path(directory) / "confirmation.sqlite")
    print(json.dumps(result, ensure_ascii=False, indent=2))
