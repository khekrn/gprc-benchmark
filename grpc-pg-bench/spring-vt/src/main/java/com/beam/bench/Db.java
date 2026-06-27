package com.beam.bench;

import org.springframework.jdbc.core.simple.JdbcClient;
import org.springframework.stereotype.Component;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Blocking data-access against the shared benchmark schema via Spring's
 * {@link JdbcClient}, executed on the calling (virtual) thread. Each call borrows
 * a connection from the HikariCP pool and returns it on the way out.
 *
 * <p>SQL is semantically identical to the other stacks; only the placeholder
 * syntax differs (JDBC {@code ?} vs the reactive drivers' {@code $1}). pgjdbc
 * server-prepares each statement and caches it per connection.
 *
 * <p>Reads go through a {@link StateCache} (Redis when {@code bench.redis.enabled},
 * else a no-op); writes that touch {@code workflow_state} evict the cached entry.
 */
@Component
public class Db {

    /** Result of {@link #getState}. {@code found == false} means no row. */
    public record StateRow(boolean found, String workflowId, String state,
                           long version, long updatedAtMicros) {
        static final StateRow MISSING = new StateRow(false, "", "", 0L, 0L);
    }

    private static final String INSERT_COMMAND_SQL =
            "INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) "
                    + "VALUES (?, ?, ?, ?, ?) RETURNING id";

    private static final String UPSERT_STATE_SQL =
            "INSERT INTO workflow_state (workflow_id, state, version, updated_at) "
                    + "VALUES (?, ?, 1, now()) "
                    + "ON CONFLICT (workflow_id) DO UPDATE SET "
                    + "state = EXCLUDED.state, "
                    + "version = workflow_state.version + 1, "
                    + "updated_at = now()";

    private static final String INSERT_OUTBOX_SQL =
            "INSERT INTO outbox (workflow_id, event_type, payload) VALUES (?, ?, ?)";

    private static final String SELECT_STATE_SQL =
            "SELECT state, version, "
                    + "(EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros "
                    + "FROM workflow_state WHERE workflow_id = ?";

    private final JdbcClient jdbc;
    private final TransactionTemplate tx;
    // Read-through cache for workflow_state: RedisStateCache when
    // bench.redis.enabled=true, else NoOpStateCache (every read hits Postgres).
    private final StateCache cache;

    Db(JdbcClient jdbc, PlatformTransactionManager txManager, StateCache cache) {
        this.jdbc = jdbc;
        this.tx = new TransactionTemplate(txManager);
        this.cache = cache;
    }

    /** Single autocommit INSERT; returns the generated id. */
    long insertCommand(String workflowId, String commandType, String payload,
                       long seq, long checksum) {
        return jdbc.sql(INSERT_COMMAND_SQL)
                .param(workflowId).param(commandType).param(payload).param(seq).param(checksum)
                .query(Long.class).single();
    }

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox), each statement sent and awaited sequentially inside one
     * BEGIN/COMMIT — the per-statement model every stack shares.
     */
    long executeTx(String workflowId, String commandType, String payload,
                   long seq, long checksum) {
        long id = tx.execute(status -> {
            long generated = jdbc.sql(INSERT_COMMAND_SQL)
                    .param(workflowId).param(commandType).param(payload).param(seq).param(checksum)
                    .query(Long.class).single();
            jdbc.sql(UPSERT_STATE_SQL).param(workflowId).param(commandType).update();
            jdbc.sql(INSERT_OUTBOX_SQL).param(workflowId).param(commandType).param(payload).update();
            return generated;
        });
        // workflow_state changed → drop the cached entry (cache-aside on write);
        // the next getState repopulates from PG. TTL is the backstop if this DEL
        // is lost. Done after commit so a rolled-back TX never evicts.
        cache.invalidate(workflowId);
        return id;
    }

    /**
     * Single-row read by workflow_id, read-through the {@link StateCache}:
     * cache hit returns immediately; on a miss we read Postgres and, if the row
     * exists, populate the cache (misses are not cached). {@link StateRow#MISSING}
     * if absent. With the NoOp cache this is byte-for-byte the original PG read.
     */
    StateRow getState(String workflowId) {
        StateRow cached = cache.get(workflowId);
        if (cached != null) {
            return cached;
        }
        StateRow row = queryState(workflowId);
        if (row.found()) {
            cache.put(workflowId, row);
        }
        return row;
    }

    /** The Postgres read behind {@link #getState} (no caching). */
    private StateRow queryState(String workflowId) {
        return jdbc.sql(SELECT_STATE_SQL).param(workflowId)
                .query((rs, rowNum) -> new StateRow(true, workflowId,
                        rs.getString(1), rs.getLong(2), rs.getLong(3)))
                .optional().orElse(StateRow.MISSING);
    }
}
