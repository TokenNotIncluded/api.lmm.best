-- Fresh installation only. Run explicitly using a module-only database role.
CREATE SCHEMA lmm_promotions;
CREATE TABLE lmm_promotions.contract (singleton boolean PRIMARY KEY CHECK (singleton), version integer NOT NULL CHECK (version = 1));
INSERT INTO lmm_promotions.contract VALUES (true, 1);
CREATE TABLE lmm_promotions.records (
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 256),
    bucket text NOT NULL CHECK (bucket IN ('claims','applications','user_totals','campaign_totals','events')),
    id text NOT NULL CHECK (length(id) BETWEEN 1 AND 256),
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object' AND octet_length(body::text) <= 16384),
    PRIMARY KEY (scope, bucket, id)
);
-- There are no user, credential, wallet, balance or ledger tables here.
