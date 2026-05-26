package com.example.app.db

import com.example.app.config.PostgresConfig
import com.example.app.domain.User
import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.pgclient.PgBuilder
import io.vertx.pgclient.PgConnectOptions
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.PoolOptions
import io.vertx.sqlclient.Row
import io.vertx.sqlclient.Tuple
import java.util.UUID

/**
 * Async PostgreSQL repository using vertx-pg-client. Every query returns a Future
 * that we suspend on with coAwait(). NO blocking JDBC anywhere.
 *
 * Things to notice:
 *   1. Prepared statements via Tuple.of(...) — parameters are bound, no SQL injection.
 *   2. Pipelining limit of 256 — multiple queries can be in-flight on one TCP connection.
 *   3. The pool is shared across all coroutines on this event loop.
 */
class UserRepository private constructor(
    private val pool: Pool,
) {
    suspend fun findById(id: String): User? {
        val rows = pool.preparedQuery(SELECT_BY_ID).execute(Tuple.of(id)).coAwait()
        return rows.firstOrNull()?.toUser()
    }

    suspend fun insert(email: String, name: String): User {
        val id = UUID.randomUUID().toString()
        val now = System.currentTimeMillis()
        pool.preparedQuery(INSERT)
            .execute(Tuple.of(id, email, name, now))
            .coAwait()
        return User(id, email, name, now)
    }

    fun close() = pool.close()

    companion object {
        private const val INSERT = """
            INSERT INTO users(id, email, name, created_at_epoch_millis)
            VALUES ($1, $2, $3, $4)
        """
        private const val SELECT_BY_ID = """
            SELECT id, email, name, created_at_epoch_millis
            FROM users WHERE id = $1
        """

        fun create(vertx: Vertx, cfg: PostgresConfig): UserRepository {
            val connect = PgConnectOptions()
                .setHost(cfg.host)
                .setPort(cfg.port)
                .setDatabase(cfg.database)
                .setUser(cfg.user)
                .setPassword(cfg.password)
                .setReconnectAttempts(2)
                .setReconnectInterval(1_000)
                .setPipeliningLimit(cfg.pipeliningLimit)
            val poolOptions = PoolOptions()
                .setMaxSize(cfg.maxSize)
                .setShared(true)                  // single pool across verticle instances
                .setName("pg-pool")
            val pool = PgBuilder.pool()
                .with(poolOptions)
                .connectingTo(connect)
                .using(vertx)
                .build()
            return UserRepository(pool)
        }

        private fun Row.toUser() = User(
            id = getString("id"),
            email = getString("email"),
            name = getString("name"),
            createdAtEpochMillis = getLong("created_at_epoch_millis"),
        )
    }
}
