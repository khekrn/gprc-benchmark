package com.example.app.db

import com.example.app.domain.NewUser
import com.example.app.domain.User
import com.example.app.domain.UserError
import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.pgclient.PgException
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.Row
import io.vertx.sqlclient.RowStream
import io.vertx.sqlclient.SqlConnection
import io.vertx.sqlclient.Tuple
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ClosedSendChannelException
import kotlinx.coroutines.channels.consumeEach
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.consumeAsFlow
import java.time.OffsetDateTime

/**
 * Coroutine-first data access.  Every public method is suspending, returns
 * a domain type, and never leaks Vert.x types into the caller.
 *
 * The repository is stateless except for the Pool reference.  Always inject;
 * never construct a Pool here.
 */
class UserRepository(private val pool: Pool) {

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
     * Server-side cursor + RowStream.  We stream rows into a Channel so the
     * caller can collect them as a Flow without ever materialising the full
     * result-set in memory.  Used by the gRPC server-streaming endpoint.
     */
    fun streamAll(emailPrefix: String?, fetchSize: Int = 100): Flow<User> {
        val channel = Channel<User>(capacity = fetchSize)
        // Acquire a dedicated connection so the transaction wrapping the
        // cursor stays valid for the lifetime of the stream.
        pool.withTransaction { conn: SqlConnection ->
            val sql = if (emailPrefix.isNullOrBlank()) SQL_STREAM_ALL else SQL_STREAM_PREFIX
            val params = if (emailPrefix.isNullOrBlank()) Tuple.tuple() else Tuple.of("$emailPrefix%")
            val stream: RowStream<Row> = conn.preparedQuery(sql)
                .createStream(fetchSize, params)
            stream.handler { row ->
                val ok = channel.trySend(rowToUser(row)).isSuccess
                if (!ok) stream.pause()       // back-pressure: stop reading PG
            }
            stream.exceptionHandler { t -> channel.close(t) }
            stream.endHandler { channel.close() }
            // Resume hook: when the consumer pulls, ask Vert.x for more rows.
            channel.invokeOnClose { stream.close() }
            io.vertx.core.Future.succeededFuture<Void>()
        }.onFailure { t -> channel.close(t) }

        return channel.consumeAsFlow()
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
     * The function takes a Vertx instance because we need a long-lived
     * dedicated connection (not from the pool).
     */
    suspend fun listenForNewUsers(vertx: Vertx): Channel<Long> {
        val channel = Channel<Long>(capacity = Channel.BUFFERED)
        val connectOptions = (pool as io.vertx.pgclient.PgPool).connectOptions
        val subscriber = io.vertx.pgclient.pubsub.PgSubscriber.subscriber(vertx, connectOptions)
        subscriber.connect().coAwait()
        subscriber.channel("users_created").handler { payload ->
            val id = payload.toLongOrNull() ?: return@handler
            try {
                channel.trySend(id)
            } catch (_: ClosedSendChannelException) { /* consumer left */ }
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
