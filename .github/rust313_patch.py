from pathlib import Path

source_path = Path("apps/api-rust/src/routes/relay_openai.rs")
source = source_path.read_text()


def replace_once(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected exactly one match, found {count}")
    return text.replace(old, new, 1)


source = replace_once(
    source,
    '''    pub fn new(pg: PgPool, upstream: OpenAiUpstreamClient, quota_per_request: i64) -> Self {
        Self {
            pg,
            upstream,
            quota_per_request: quota_per_request.max(1),
        }
    }

    async fn token_for_auth(
''',
    '''    pub fn new(pg: PgPool, upstream: OpenAiUpstreamClient, quota_per_request: i64) -> Self {
        Self {
            pg,
            upstream,
            quota_per_request: quota_per_request.max(1),
        }
    }

    async fn retry_times(&self) -> Result<usize, OpenAiRelayFailure> {
        let value = sqlx::query_scalar::<_, Option<String>>(
            "SELECT value FROM options WHERE key = 'RetryTimes'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| internal_failure())?
        .flatten();
        Ok(value
            .as_deref()
            .and_then(|value| value.trim().parse::<usize>().ok())
            .unwrap_or(0))
    }

    async fn token_for_auth(
''',
    "insert retry_times reader",
)

source = replace_once(
    source,
    '''    async fn reserve(
        &self,
        request: &OpenAiRelayRequest,
    ) -> Result<Reservation, OpenAiRelayFailure> {
''',
    '''    async fn reserve(
        &self,
        request: &OpenAiRelayRequest,
        excluded_channel_ids: &[i64],
    ) -> Result<Reservation, OpenAiRelayFailure> {
''',
    "extend reserve signature",
)

source = replace_once(
    source,
    '''                   AND ($3::BIGINT IS NULL OR c.id=$3)
                   AND ($3::BIGINT IS NOT NULL OR COALESCE(c.status,1)=1)
               ORDER BY COALESCE(a.priority,0) DESC, COALESCE(a.weight,0) DESC, c.id
''',
    '''                   AND ($3::BIGINT IS NULL OR c.id=$3)
                   AND (
                       $3::BIGINT IS NOT NULL
                       OR (
                           COALESCE(c.status,1)=1
                           AND NOT (c.id = ANY($4::BIGINT[]))
                       )
                   )
               ORDER BY COALESCE(a.priority,0) DESC, COALESCE(a.weight,0) DESC, c.id
''',
    "exclude failed channels from selection",
)

source = replace_once(
    source,
    '''        .bind(selection_model)
        .bind(specific_channel_id)
        .fetch_optional(&mut *tx)
''',
    '''        .bind(selection_model)
        .bind(specific_channel_id)
        .bind(excluded_channel_ids)
        .fetch_optional(&mut *tx)
''',
    "bind failed-channel exclusion",
)

source = replace_once(
    source,
    '''    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        let reservation = self.reserve(&request).await?;
        match self.upstream.forward(&reservation.target, &request).await {
            Ok(result) => {
                if let Err(error) = self.log_success(&reservation, &request).await {
                    self.refund(
                        reservation.token_id,
                        reservation.user_id,
                        reservation.channel_id,
                    )
                    .await?;
                    return Err(error);
                }
                Ok(result)
            }
            Err(error) => {
                self.refund(
                    reservation.token_id,
                    reservation.user_id,
                    reservation.channel_id,
                )
                .await?;
                Err(error)
            }
        }
    }
''',
    '''    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        let credential =
            relay_token_credential(&request.headers).ok_or_else(unauthorized_failure)?;
        let specific_channel = credential
            .channel_suffix
            .as_deref()
            .is_some_and(|channel| !channel.is_empty());
        let retry_times = if specific_channel {
            0
        } else {
            self.retry_times().await?
        };
        let mut excluded_channel_ids = Vec::new();

        for attempt in 0..=retry_times {
            let reservation = self.reserve(&request, &excluded_channel_ids).await?;
            match self.upstream.forward(&reservation.target, &request).await {
                Ok(result) => {
                    if let Err(error) = self.log_success(&reservation, &request).await {
                        self.refund(
                            reservation.token_id,
                            reservation.user_id,
                            reservation.channel_id,
                        )
                        .await?;
                        return Err(error);
                    }
                    return Ok(result);
                }
                Err(error) => {
                    self.refund(
                        reservation.token_id,
                        reservation.user_id,
                        reservation.channel_id,
                    )
                    .await?;
                    if specific_channel
                        || attempt >= retry_times
                        || !is_first_output_retry_failure(&error)
                    {
                        return Err(error);
                    }
                    excluded_channel_ids.push(reservation.channel_id);
                }
            }
        }

        Err(no_channel_failure())
    }
''',
    "install per-attempt first-output retry loop",
)

source = replace_once(
    source,
    '''fn openai_upstream_request_failure(error: RelayHttpError) -> OpenAiRelayFailure {
    match error {
''',
    '''fn is_first_output_retry_failure(failure: &OpenAiRelayFailure) -> bool {
    failure.status == StatusCode::GATEWAY_TIMEOUT
        && failure.code == "upstream_timeout"
        && failure.message == "upstream first response timeout"
}

fn openai_upstream_request_failure(error: RelayHttpError) -> OpenAiRelayFailure {
    match error {
''',
    "add narrow retry classifier",
)

source_path.write_text(source)

test_path = Path("apps/api-rust/tests/relay_openai_specific_channel_pg.rs")
test = test_path.read_text()
marker = "postgres_first_output_timeout_retries_next_channel_and_commits_only_success"
if marker in test:
    raise SystemExit(f"{marker}: test already exists")

test += r'''

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_first_output_timeout_retries_next_channel_and_commits_only_success()
-> TestResult {
    let Some((admin, pool, schema)) = isolated_pool().await? else {
        eprintln!("skipping OpenAI failover PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };

    let mut first = spawn_upstream(MockUpstreamBehavior::RoleOnlySseThenStall).await?;
    let mut second = spawn_upstream(MockUpstreamBehavior::JsonSuccess).await?;

    let result = async {
        create_minimal_relay_schema(&pool).await?;
        sqlx::query("INSERT INTO options (key,value) VALUES ('RetryTimes','1')")
            .execute(&pool)
            .await?;
        sqlx::query("INSERT INTO users (id,status,quota,role) VALUES (1,1,100,1)")
            .execute(&pool)
            .await?;
        sqlx::query(
            "INSERT INTO tokens (id,user_id,status,expired_time,remain_quota,unlimited_quota,allow_ips,key,\"group\") VALUES (11,1,1,-1,100,FALSE,'','tenant','default')",
        )
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO channels (id,status,base_url,key) VALUES (1,1,$1,'first-key'),(2,1,$2,'second-key')",
        )
        .bind(&first.base_url)
        .bind(&second.base_url)
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-4o',1,TRUE,100,100),('default','gpt-4o',2,TRUE,1,1)",
        )
        .execute(&pool)
        .await?;

        let service = PgOpenAiRelayService::new(
            pool.clone(),
            OpenAiUpstreamClient::new(first_output_test_client()?),
            1,
        );
        let router = openai_relay_router(OpenAiRelayHttpState::new(Arc::new(service), "test"));

        assert_eq!(
            relay_request(&router, "Bearer sk-tenant", true).await?,
            StatusCode::OK,
            "RetryTimes=1 must fail over after a pre-visible first-output timeout"
        );
        assert!(
            timeout(Duration::from_secs(1), first.received.recv())
                .await?
                .is_some(),
            "the highest-priority channel did not receive the first attempt"
        );
        assert!(
            timeout(Duration::from_secs(1), second.received.recv())
                .await?
                .is_some(),
            "the retry did not reach the next eligible channel"
        );
        assert!(
            first.received.try_recv().is_err() && second.received.try_recv().is_err(),
            "each eligible channel must be attempted at most once for this request"
        );

        let user: (i64, i64, i64) =
            sqlx::query_as("SELECT quota,used_quota,request_count FROM users WHERE id=1")
                .fetch_one(&pool)
                .await?;
        assert_eq!(user, (99, 1, 1), "only the successful attempt may remain charged");

        let token: (i64, i64) =
            sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
                .fetch_one(&pool)
                .await?;
        assert_eq!(token, (99, 1), "token accounting must reflect one successful attempt");

        let channel_usage: Vec<(i64, i64)> =
            sqlx::query_as("SELECT id,COALESCE(used_quota,0) FROM channels ORDER BY id")
                .fetch_all(&pool)
                .await?;
        assert_eq!(
            channel_usage,
            vec![(1, 0), (2, 1)],
            "the failed channel must be refunded and the successful channel charged once"
        );

        let logs: Vec<(i64, i64)> =
            sqlx::query_as("SELECT channel_id,quota FROM logs WHERE type=2 ORDER BY channel_id")
                .fetch_all(&pool)
                .await?;
        assert_eq!(
            logs,
            vec![(2, 1)],
            "only the successful retry may create a success usage log"
        );
        Ok::<(), Box<dyn std::error::Error>>(())
    }
    .await;

    first.task.abort();
    second.task.abort();
    drop(pool);
    let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await;
    drop(admin);

    result?;
    cleanup?;
    Ok(())
}
'''

test_path.write_text(test)
