package com.beam.bench;

import org.springframework.jdbc.core.simple.JdbcClient;
import org.springframework.stereotype.Component;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;

/**
 * Blocking JDBC data-access against the shared benchmark schema, executed on
 * the calling (virtual) thread. Each method borrows a connection from the
 * HikariCP pool and returns it on the way out.
 *
 * SQL is semantically identical to the other stacks; only the placeholder
 * syntax differs (JDBC {@code ?} vs the reactive drivers' {@code $1}). pgjdbc
 * server-prepares each statement and caches it per connection.
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

    private final DataSource ds;
    private final JdbcClient jdbc;
    private final TransactionTemplate tx;
    // Default data layer is Spring's JdbcClient (the "spring-jdbc" abstraction —
    // fluent, ~1-5% over raw JDBC, far cheaper than JPA/Exposed). Set DB_IMPL=raw
    // to fall back to hand-written raw JDBC (used for the abstraction-cost A/B).
    private final boolean useJdbcClient = !"raw".equalsIgnoreCase(System.getenv("DB_IMPL"));

    Db(DataSource ds, JdbcClient jdbc, PlatformTransactionManager txManager) {
        this.ds = ds;
        this.jdbc = jdbc;
        this.tx = new TransactionTemplate(txManager);
    }

    /** Single autocommit INSERT; returns the generated id. */
    long insertCommand(String workflowId, String commandType, String payload,
                       long seq, long checksum) throws SQLException {
        if (useJdbcClient) {
            return jdbc.sql(INSERT_COMMAND_SQL)
                    .param(workflowId).param(commandType).param(payload).param(seq).param(checksum)
                    .query(Long.class).single();
        }
        try (Connection c = ds.getConnection();
             PreparedStatement ps = c.prepareStatement(INSERT_COMMAND_SQL)) {
            ps.setString(1, workflowId);
            ps.setString(2, commandType);
            ps.setString(3, payload);
            ps.setLong(4, seq);
            ps.setLong(5, checksum);
            try (ResultSet rs = ps.executeQuery()) {
                rs.next();
                return rs.getLong(1);
            }
        }
    }

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox), each statement sent and awaited sequentially inside one
     * BEGIN/COMMIT — the per-statement model every stack shares.
     */
    long executeTx(String workflowId, String commandType, String payload,
                   long seq, long checksum) throws SQLException {
        if (useJdbcClient) {
            return tx.execute(status -> {
                long id = jdbc.sql(INSERT_COMMAND_SQL)
                        .param(workflowId).param(commandType).param(payload).param(seq).param(checksum)
                        .query(Long.class).single();
                jdbc.sql(UPSERT_STATE_SQL).param(workflowId).param(commandType).update();
                jdbc.sql(INSERT_OUTBOX_SQL).param(workflowId).param(commandType).param(payload).update();
                return id;
            });
        }
        try (Connection c = ds.getConnection()) {
            c.setAutoCommit(false);
            try {
                long id;
                try (PreparedStatement ps = c.prepareStatement(INSERT_COMMAND_SQL)) {
                    ps.setString(1, workflowId);
                    ps.setString(2, commandType);
                    ps.setString(3, payload);
                    ps.setLong(4, seq);
                    ps.setLong(5, checksum);
                    try (ResultSet rs = ps.executeQuery()) {
                        rs.next();
                        id = rs.getLong(1);
                    }
                }
                try (PreparedStatement ps = c.prepareStatement(UPSERT_STATE_SQL)) {
                    ps.setString(1, workflowId);
                    ps.setString(2, commandType);
                    ps.executeUpdate();
                }
                try (PreparedStatement ps = c.prepareStatement(INSERT_OUTBOX_SQL)) {
                    ps.setString(1, workflowId);
                    ps.setString(2, commandType);
                    ps.setString(3, payload);
                    ps.executeUpdate();
                }
                c.commit();
                return id;
            } catch (SQLException | RuntimeException e) {
                c.rollback();
                throw e;
            } finally {
                c.setAutoCommit(true);
            }
        }
    }

    /** Single-row read by workflow_id; {@link StateRow#MISSING} if absent. */
    StateRow getState(String workflowId) throws SQLException {
        if (useJdbcClient) {
            return jdbc.sql(SELECT_STATE_SQL).param(workflowId)
                    .query((rs, rowNum) -> new StateRow(true, workflowId,
                            rs.getString(1), rs.getLong(2), rs.getLong(3)))
                    .optional().orElse(StateRow.MISSING);
        }
        try (Connection c = ds.getConnection();
             PreparedStatement ps = c.prepareStatement(SELECT_STATE_SQL)) {
            ps.setString(1, workflowId);
            try (ResultSet rs = ps.executeQuery()) {
                if (!rs.next()) {
                    return StateRow.MISSING;
                }
                return new StateRow(true, workflowId,
                        rs.getString(1), rs.getLong(2), rs.getLong(3));
            }
        }
    }
}
