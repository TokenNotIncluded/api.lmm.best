-- Fresh installation only. Run explicitly using a module-only database role.
CREATE SCHEMA lmm_assistant;
CREATE TABLE lmm_assistant.contract (singleton boolean PRIMARY KEY CHECK (singleton), version integer NOT NULL CHECK (version = 1));
INSERT INTO lmm_assistant.contract VALUES (true, 1);
CREATE TABLE lmm_assistant.records (
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 256),
    bucket text NOT NULL CHECK (bucket IN ('sessions','calls','preferences','entries')),
    id text NOT NULL CHECK (length(id) BETWEEN 1 AND 256),
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object' AND octet_length(body::text) <= 16384),
    PRIMARY KEY (scope, bucket, id)
);
-- There are no user, credential, wallet, balance or ledger tables here.
