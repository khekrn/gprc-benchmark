package com.example.app.db

import com.example.app.domain.NewUser
import com.example.app.domain.User
import com.example.app.domain.UserError
import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.pgclient.PgConnectOptions
import io.vertx.pgclient.PgException
import io.vertx.pgclient.pubsub.PgSubscriber
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.Row
import io.vertx.sqlclient.Tuple
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import java.time.OffsetDateTime

/**
 * Coroutine-first data access.  Every public method is suspending, returns
 * a domain type, and never leaks Vert.x types into the caller.
 *
 * The repository is stateless except for the Pool reference.  Always inject;
 * never construct a Pool here.  [connectOptions] is only needed for the
 * LISTEN/NOTIFY hook, which requires a dedicated (non-pooled) connection.
 */
class UserRepository(
    private val pool: Pool,
    private val connectOptions: PgConnectOptions? = null,
) {

    // --------- single-row reads ----------------------------------------

    suspend fun findById(id: Long): User? {
        val rows = pool.preparedQuery(SQL_FIND_BY_ID)
            .execute(Tuple.of(id))
            .coAwait()
        return rows.firstOrNull()?.let(::rowToUser)
    }

    suspend fun findByEmail(email: String): User? {
        val rows = pool.preparedQuery(SQL_FIND_BY_EMAIL)
            .execute(Tuple.of(email))
            .coAwait()
        return rows.firstOrNull()?.let(::rowToUser)
    }

    // --------- write -----------------------------------------------------

    suspend fun create(input: NewUser): User {
        try {
            val row = pool.preparedQuery(SQL_INSERT)
                .execute(Tuple.of(input.email, input.fullName))
                .coAwait()
                .first()
            return rowToUser(row)
        } catch (e: PgException) {
            // 23505 = unique_violation
            if (e.sqlState == "23505") throw UserError.DuplicateEmail(input.email)
            throw e
        }
    }

    // --------- streaming reads ------------------------------------------

    /**
     * Server-side cursor exposed as a cold, back-pressured [Flow].  We hold a
     * dedicated connection + transaction open for the lifetime of the stream
     * and read the cursor in [fetchSize] batches.  Because `emit` suspends
     * until the collector is ready, we never fetch a batch we cannot hand off:
     * this is end-to-end back-pressure with no manual pause/resume bookkeeping.
     * Used by the gRPC server-streaming endpoint and the NDJSON REST endpoint.
     */
    fun streamAll(emailPrefix: String?, fetchSize: Int = 100): Flow<User> = flow {
        val sql = if (emailPrefix.isNullOrBlank()) SQL_STREAM_ALL else SQL_STREAM_PREFIX
        val params = if (emailPrefix.isNullOrBlank()) Tuple.tuple() else Tuple.of("$emailPrefix%")
        val conn = pool.connection.coAwait()
        try {
            val tx = conn.begin().coAwait()
            val cursor = conn.prepare(sql).coAwait().cursor(params)
            try {
                do {
                    val rows = cursor.read(fetchSize).coAwait()
                    for (row in rows) emit(rowToUser(row))
                } while (cursor.hasMore())
            } finally {
                cursor.close().coAwait()
            }
            tx.commit().coAwait()
        } finally {
            conn.close().coAwait()
        }
    }

    // --------- batch ------------------------------------------------------

    /**
     * Insert many users in one round-trip.  preparedQuery.executeBatch is
     * pipelined; PG sees N parse-bind-execute frames as a single stream.
     */
    suspend fun createMany(inputs: List<NewUser>): List<User> {
        if (inputs.isEmpty()) return emptyList()
        val tuples = inputs.map { Tuple.of(it.email, it.fullName) }
        val rows = pool.preparedQuery(SQL_INSERT).executeBatch(tuples).coAwait()
        return buildList {
            var r = rows
            while (r != null) {
                r.forEach { add(rowToUser(it)) }
                r = r.next()
            }
        }
    }

    // --------- LISTEN/NOTIFY hook --------------------------------------

    /**
     * Subscribe to Postgres NOTIFY events on `users_created`.  Each id pushed
     * to the channel was just inserted by *any* client of the database.
     *
     * Requires [connectOptions]; the subscriber owns a long-lived dedicated
     * connection (not from the pool).
     */
    suspend fun listenForNewUsers(vertx: Vertx): Channel<Long> {
        val opts = connectOptions
            ?: error("UserRepository was built without PgConnectOptions; LISTEN/NOTIFY unavailable")
        val channel = Channel<Long>(capacity = Channel.BUFFERED)
        val subscriber = PgSubscriber.subscriber(vertx, opts)
        subscriber.connect().coAwait()
        subscriber.channel("users_created").handler { payload ->
            val id = payload.toLongOrNull() ?: return@handler
            channel.trySend(id)   // drop if the consumer is gone; never throws
        }
        channel.invokeOnClose { subscriber.close() }
        return channel
    }

    // --------- row mapping ----------------------------------------------

    private fun rowToUser(row: Row): User =
        User(
            id        = row.getLong("id"),
            email     = row.getString("email"),
            fullName  = row.getString("full_name"),
            createdAt = row.getOffsetDateTime("created_at") ?: OffsetDateTime.now(),
        )

    private companion object {
        const val SQL_FIND_BY_ID =
            "SELECT id, email, full_name, created_at FROM users WHERE id = $1"
        const val SQL_FIND_BY_EMAIL =
            "SELECT id, email, full_name, created_at FROM users WHERE email = $1"
        const val SQL_INSERT =
            "INSERT INTO users (email, full_name) VALUES ($1, $2) " +
            "RETURNING id, email, full_name, created_at"
        const val SQL_STREAM_ALL =
            "SELECT id, email, full_name, created_at FROM users ORDER BY id"
        const val SQL_STREAM_PREFIX =
            "SELECT id, email, full_name, created_at FROM users " +
            "WHERE email LIKE $1 ORDER BY id"
    }
}
