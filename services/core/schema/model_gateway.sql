-- Fresh-install model evidence. This stores hashes and optional provider
-- counters, never prompts, output text, credentials, balances or estimates.
CREATE SCHEMA core_model;
CREATE TABLE core_model.output_checkpoints (
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    sequence BIGINT NOT NULL CHECK (sequence BETWEEN 1 AND 65536),
    packet_sha256 BYTEA NOT NULL CHECK (octet_length(packet_sha256)=32),
    packet_bytes BIGINT NOT NULL CHECK (packet_bytes BETWEEN 0 AND 134217728),
    prepared_bytes BIGINT NOT NULL CHECK (prepared_bytes BETWEEN 0 AND 1073741824),
    provider_usage JSONB NOT NULL CHECK (jsonb_typeof(provider_usage)='object'
        AND pg_column_size(provider_usage)<=2048),
    terminal_observed BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(request_id,sequence)
);
CREATE TRIGGER model_checkpoints_immutable BEFORE UPDATE OR DELETE
    ON core_model.output_checkpoints FOR EACH ROW
    EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER model_checkpoints_no_truncate BEFORE TRUNCATE
    ON core_model.output_checkpoints FOR EACH STATEMENT
    EXECUTE FUNCTION core_charging.no_delete();

-- Runtime has EXECUTE only. The dispatch worker capability and the original
-- charging request bind every observation to its immutable payer/price. Exact
-- retries resolve a lost COMMIT acknowledgement without another upstream call.
CREATE FUNCTION core_model.checkpoint_output(p_request_id TEXT,
    p_worker BYTEA,p_sequence BIGINT,p_packet_sha256 BYTEA,
    p_packet_bytes BIGINT,p_provider_usage JSONB,p_terminal_observed BOOLEAN)
RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE request core_charging.requests%ROWTYPE;
    previous core_model.output_checkpoints%ROWTYPE;
    duplicate core_model.output_checkpoints%ROWTYPE;
    at_time BIGINT;
BEGIN
    IF current_setting('transaction_isolation')<>'serializable' THEN
        RAISE EXCEPTION 'model evidence requires serializable' USING ERRCODE='25001';
    END IF;
    IF p_request_id IS NULL OR p_worker IS NULL OR p_sequence IS NULL
        OR p_packet_sha256 IS NULL OR p_packet_bytes IS NULL
        OR p_provider_usage IS NULL OR p_terminal_observed IS NULL
        OR octet_length(p_worker)<>32 OR octet_length(p_packet_sha256)<>32
        OR p_sequence NOT BETWEEN 1 AND 65536
        OR p_packet_bytes NOT BETWEEN 0 AND 134217728
        OR jsonb_typeof(p_provider_usage)<>'object'
        OR pg_column_size(p_provider_usage)>2048 THEN
        RAISE EXCEPTION 'invalid model evidence' USING ERRCODE='23514';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('core_charging:'||p_request_id,0));
    SELECT * INTO STRICT request FROM core_charging.requests
        WHERE id=p_request_id FOR UPDATE;
    IF request.worker_digest<>sha256(p_worker) THEN
        RAISE EXCEPTION 'wrong model worker' USING ERRCODE='42501';
    END IF;
    SELECT * INTO duplicate FROM core_model.output_checkpoints
        WHERE request_id=p_request_id AND sequence=p_sequence;
    IF FOUND THEN
        IF (duplicate.packet_sha256,duplicate.packet_bytes,duplicate.provider_usage,
            duplicate.terminal_observed) IS DISTINCT FROM
           (p_packet_sha256,p_packet_bytes,p_provider_usage,p_terminal_observed) THEN
            RAISE EXCEPTION 'conflicting model evidence' USING ERRCODE='23505';
        END IF;
        RETURN;
    END IF;
    at_time:=floor(extract(epoch FROM clock_timestamp()))::bigint;
    IF request.state<>'streaming' OR at_time>=request.lease_until
        OR at_time>=request.hard_deadline THEN
        RAISE EXCEPTION 'model worker is not live' USING ERRCODE='42501';
    END IF;
    SELECT * INTO previous FROM core_model.output_checkpoints
        WHERE request_id=p_request_id ORDER BY sequence DESC LIMIT 1;
    IF p_sequence<>coalesce(previous.sequence,0)+1
        OR (coalesce(previous.terminal_observed,FALSE) AND NOT p_terminal_observed)
        OR coalesce(previous.prepared_bytes,0)+p_packet_bytes>1073741824 THEN
        RAISE EXCEPTION 'invalid model evidence sequence' USING ERRCODE='23514';
    END IF;
    INSERT INTO core_model.output_checkpoints(request_id,sequence,packet_sha256,
        packet_bytes,prepared_bytes,provider_usage,terminal_observed)
        VALUES(p_request_id,p_sequence,p_packet_sha256,p_packet_bytes,
            coalesce(previous.prepared_bytes,0)+p_packet_bytes,p_provider_usage,
            p_terminal_observed);
END $$;
-- Internal bounded recovery discovery. Only a validated protocol terminal
-- observation with both provider counters can authorize automatic settlement.
-- Partial observations and missing usage never appear in this result.
CREATE FUNCTION core_model.completed_streams(p_limit INTEGER)
RETURNS TABLE(request_id TEXT,revision BIGINT,provider_usage JSONB,sequence BIGINT)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    IF p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'invalid model recovery limit' USING ERRCODE='23514';
    END IF;
    RETURN QUERY SELECT r.id,r.revision,e.provider_usage,e.sequence
        FROM core_charging.requests r
        CROSS JOIN LATERAL (SELECT c.provider_usage,c.sequence
            FROM core_model.output_checkpoints c
            WHERE c.request_id=r.id AND c.terminal_observed
            ORDER BY c.sequence DESC LIMIT 1) e
        WHERE r.state='reconcile'
            AND jsonb_typeof(e.provider_usage->'input_tokens')='number'
            AND jsonb_typeof(e.provider_usage->'output_tokens')='number'
        ORDER BY r.id LIMIT p_limit;
END $$;
REVOKE ALL ON SCHEMA core_model FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA core_model FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA core_model FROM PUBLIC;
