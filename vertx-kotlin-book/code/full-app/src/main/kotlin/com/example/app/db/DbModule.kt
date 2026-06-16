package com.example.app.db

import com.example.app.config.AppConfig
import io.vertx.core.Vertx
import io.vertx.pgclient.PgBuilder
import io.vertx.pgclient.PgConnectOptions
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.PoolOptions

/**
 * One Pool per process.  The Pool is event-loop aware: each acquire returns
 * a connection bound to the caller's event loop, so under load there's no
 * cross-thread hand-off.  See chapter 9.
 */
object DbModule {

    fun pool(vertx: Vertx, db: AppConfig.DbConfig): Pool {
        val connect = PgConnectOptions()
            .setHost(db.host)
            .setPort(db.port)
            .setDatabase(db.database)
            .setUser(db.user)
            .setPassword(db.password)
            // Pipelining lets one connection have N in-flight requests.
            // The wire protocol multiplexes them; replies come back in order.
            .setPipeliningLimit(db.pipeliningLimit)
            // Cache prepared statements per connection.  Speed-up on hot queries.
            .setCachePreparedStatements(true)
            .setPreparedStatementCacheMaxSize(256)
            .setReconnectAttempts(10)
            .setReconnectInterval(500)

        val poolOpts = PoolOptions()
            .setMaxSize(db.poolMaxSize)
            .setShared(true)
            .setName("app-pg-pool")

        return PgBuilder.pool()
            .with(poolOpts)
            .connectingTo(connect)
            .using(vertx)
            .build()
    }
}
