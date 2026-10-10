SELECT jsonb_build_object(
    'at',clock_timestamp(),
    'client_backends',(SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend'),
    'activity',(SELECT jsonb_agg(to_jsonb(x)) FROM (
        SELECT datname,usename,application_name,state,wait_event_type,wait_event,count(*) AS sessions
        FROM pg_stat_activity WHERE backend_type='client backend'
        GROUP BY datname,usename,application_name,state,wait_event_type,wait_event
    ) x),
    'database',(SELECT to_jsonb(s) FROM pg_stat_database s WHERE datname=current_database()),
    'wal',(SELECT to_jsonb(s) FROM pg_stat_wal s),
    'checkpointer',(SELECT to_jsonb(s) FROM pg_stat_checkpointer s),
    'io',(SELECT jsonb_agg(to_jsonb(s)) FROM pg_stat_io s)
)::text;
