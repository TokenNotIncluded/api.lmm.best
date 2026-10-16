-- Explicit offline provisioning for a NEW isolated PostgreSQL 18 database.
-- Run as its database administrator before lmm-core init-db. No startup hook.
-- Existing roles reject the transaction; this is not a role repair/reset tool.
\set ON_ERROR_STOP on
-- @mark deployment.database-authority role=installer
BEGIN;
SET LOCAL statement_timeout = '5s';
SELECT current_database() AS core_database \gset
CREATE ROLE lmm_core_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS;
CREATE ROLE lmm_core_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS;
REVOKE CREATE, TEMPORARY ON DATABASE :"core_database" FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT, CREATE ON DATABASE :"core_database" TO lmm_core_owner;
GRANT USAGE, CREATE ON SCHEMA public TO lmm_core_owner;
GRANT CONNECT ON DATABASE :"core_database" TO lmm_core_runtime;
COMMIT;
-- The runtime role has no password yet. Set it separately with psql \password,
-- or a private stdin-only administrative session; never a password argv option.
-- The install URL authenticates the offline administrator and uses
-- options=-c%20role%3Dlmm_core_owner. Do not grant that role to the runtime.
