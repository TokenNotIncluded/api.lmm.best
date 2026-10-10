-- Read the repository's real ledger. No substitute balance table is created.
SELECT jsonb_build_object(
  'operations', (SELECT count(*) FROM core_billing.ledger_operations),
  'journals', (SELECT count(*) FROM core_billing.ledger_journals),
  'entries', (SELECT count(*) FROM core_billing.ledger_entries),
  'net_units', (SELECT coalesce(sum(balance_units), 0)::text FROM core_billing.balance_state),
  'unbalanced_journals', (SELECT count(*) FROM (
      SELECT j.id FROM core_billing.ledger_journals j
      LEFT JOIN core_billing.ledger_entries e ON e.journal_id = j.id
      GROUP BY j.id HAVING count(e.journal_id) < 2 OR sum(e.delta_units) IS DISTINCT FROM 0::numeric
  ) q),
  'projection_mismatches', (SELECT count(*) FROM core_billing.balance_state b
      LEFT JOIN (SELECT ledger_account_id, sum(delta_units) AS units, count(*) AS revision
                 FROM core_billing.ledger_entries GROUP BY ledger_account_id) e
      ON e.ledger_account_id = b.ledger_account_id
      WHERE b.balance_units IS DISTINCT FROM coalesce(e.units, 0)
         OR b.revision IS DISTINCT FROM coalesce(e.revision, 0)),
  'negative_customer_buckets', (SELECT count(*) FROM core_billing.balance_state b
      JOIN core_billing.ledger_accounts a ON a.id = b.ledger_account_id
      WHERE a.bucket <> 'clearing' AND b.balance_units < 0),
  'reserved_units', (SELECT coalesce(sum(b.balance_units), 0)::text
      FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a ON a.id=b.ledger_account_id
      WHERE a.bucket='reserved'),
  'balances', (SELECT jsonb_agg(to_jsonb(b) ORDER BY b.ledger_account_id) FROM core_billing.balance_state b),
  'operation_digest', (SELECT md5(coalesce(string_agg(scope||':'||operation_key||':'||request::text||':'||result::text,
      E'\n' ORDER BY scope,operation_key), '')) FROM core_billing.ledger_operations)
)::text;
-- operation_digest is a small-test comparison checksum, NOT a backup authenticity signature.
