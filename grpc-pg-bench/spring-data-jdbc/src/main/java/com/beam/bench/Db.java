package com.beam.bench;

import org.springframework.stereotype.Component;
import org.springframework.transaction.annotation.Transactional;

import java.time.Instant;

/**
 * Data-access layer over Spring Data JDBC repositories, executed on the calling
 * (virtual) thread. Mirrors spring-vt's {@code Db} API one-for-one — same three
 * operations, same SQL, same HikariCP pool — but goes through the repository
 * abstraction (entities + {@code save()} + a {@code @Modifying} upsert) instead
 * of the fluent {@code JdbcClient}. This is the apples-to-apples "Spring Data
 * JDBC repository cost vs JdbcClient" comparison.
 */
@Component
public class Db {

    /** Result of {@link #getState}. {@code found == false} means no row. */
    public record StateRow(boolean found, String workflowId, String state,
                           long version, long updatedAtMicros) {
        static final StateRow MISSING = new StateRow(false, "", "", 0L, 0L);
    }

    private final CommandRepository commands;
    private final WorkflowStateRepository states;
    private final OutboxRepository outbox;

    Db(CommandRepository commands, WorkflowStateRepository states, OutboxRepository outbox) {
        this.commands = commands;
        this.states = states;
        this.outbox = outbox;
    }

    /** Single autocommit INSERT via {@code save()}; returns the generated id. */
    long insertCommand(String workflowId, String commandType, String payload,
                       long seq, long checksum) {
        Command saved = commands.save(
                new Command(null, workflowId, commandType, payload, seq, checksum));
        return saved.id();
    }

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox), each statement sent and awaited sequentially inside one
     * Spring-managed transaction — the per-statement model every stack shares.
     * {@code @Transactional} makes Spring open the BEGIN/COMMIT around all three
     * repository calls (they enlist on the same HikariCP connection).
     */
    @Transactional
    public long executeTx(String workflowId, String commandType, String payload,
                          long seq, long checksum) {
        long id = commands.save(
                new Command(null, workflowId, commandType, payload, seq, checksum)).id();
        states.upsert(workflowId, commandType);
        outbox.save(new OutboxEvent(null, workflowId, commandType, payload));
        return id;
    }

    /** Single-row read by primary key; {@link StateRow#MISSING} if absent. */
    StateRow getState(String workflowId) {
        return states.findById(workflowId)
                .map(s -> new StateRow(true, s.workflowId(), s.state(), s.version(),
                        toMicros(s.updatedAt())))
                .orElse(StateRow.MISSING);
    }

    /** Unix micros from an Instant — matches the BIGINT the SQL stacks return. */
    private static long toMicros(Instant ts) {
        return ts.getEpochSecond() * 1_000_000L + ts.getNano() / 1_000L;
    }
}
