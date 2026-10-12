-- A07 candidate for S11, applied ONLY to fresh, isolated test databases here.
-- Install after model_gateway.sql, then grant EXECUTE to the existing recovery
-- role. This fixture is not registered in core_schema or ordinary startup.
CREATE FUNCTION core_model.completed_streams_after(p_limit INTEGER, p_after TEXT)
RETURNS TABLE(request_id TEXT,revision BIGINT,provider_usage JSONB,sequence BIGINT)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    IF p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 1000
        OR (p_after IS NOT NULL AND (octet_length(p_after) NOT BETWEEN 1 AND 128
            OR p_after !~ '^[A-Za-z0-9_.:/-]+$')) THEN
        RAISE EXCEPTION 'invalid model recovery page' USING ERRCODE='23514';
    END IF;
    RETURN QUERY SELECT r.id,r.revision,e.provider_usage,e.sequence
        FROM core_charging.requests r
        CROSS JOIN LATERAL (SELECT c.provider_usage,c.sequence
            FROM core_model.output_checkpoints c
            WHERE c.request_id=r.id AND c.terminal_observed
            ORDER BY c.sequence DESC LIMIT 1) e
        WHERE r.state='reconcile'
            AND (p_after IS NULL OR r.id>p_after)
            AND jsonb_typeof(e.provider_usage->'input_tokens')='number'
            AND jsonb_typeof(e.provider_usage->'output_tokens')='number'
        ORDER BY r.id LIMIT p_limit;
END $$;
REVOKE ALL ON FUNCTION core_model.completed_streams_after(INTEGER,TEXT) FROM PUBLIC;
