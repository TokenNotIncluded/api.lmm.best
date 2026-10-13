-- Fresh isolated operations database only; installed offline with owner authority.
CREATE SCHEMA lmm_operations;
CREATE TABLE lmm_operations.contract (version integer PRIMARY KEY CHECK (version=1), fingerprint text NOT NULL);
INSERT INTO lmm_operations.contract VALUES (1,'');
CREATE SEQUENCE lmm_operations.publication_id MAXVALUE 9007199254740991;
CREATE TABLE lmm_operations.publications (
 id bigint PRIMARY KEY DEFAULT nextval('lmm_operations.publication_id') CHECK (id BETWEEN 1 AND 9007199254740991),
 version text NOT NULL CHECK (octet_length(version) BETWEEN 1 AND 64 AND version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]*$'),
 revision integer NOT NULL CHECK (revision BETWEEN 1 AND 10000),
 content text NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 32768),
 published_at bigint NOT NULL CHECK (published_at BETWEEN 1 AND 8640000000000),
 published_by bigint NOT NULL CHECK (published_by BETWEEN 1 AND 9007199254740991),
 UNIQUE(version,revision), UNIQUE(id,revision)
);
CREATE TABLE lmm_operations.reads (
 user_id bigint NOT NULL CHECK (user_id BETWEEN 1 AND 9007199254740991),
 note_id bigint NOT NULL,
 revision integer NOT NULL,
 PRIMARY KEY(user_id,note_id,revision),
 FOREIGN KEY(note_id,revision) REFERENCES lmm_operations.publications(id,revision)
);
REVOKE ALL ON SCHEMA lmm_operations FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA lmm_operations FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA lmm_operations FROM PUBLIC;
