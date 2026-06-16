package com.example.app.db

import io.vertx.kotlin.coroutines.coAwait
import io.vertx.sqlclient.Pool
import org.slf4j.LoggerFactory

/**
 * Minimal "migration" runner: reads classpath:db/migration/V*__*.sql in
 * lexicographic order and executes each as a single batch.  Idempotent
 * statements are required (CREATE TABLE IF NOT EXISTS etc.).  For a real
 * production system, prefer Flyway/Liquibase via a one-shot worker verticle.
 */
object DbMigrator {
    private val log = LoggerFactory.getLogger(DbMigrator::class.java)

    suspend fun migrate(pool: Pool) {
        val resources = listOf("db/migration/V1__schema.sql")
        for (path in resources) {
            val sql = loader(path)
            log.info("Applying migration {}", path)
            pool.query(sql).execute().coAwait()
        }
    }

    private fun loader(path: String): String =
        Thread.currentThread().contextClassLoader.getResourceAsStream(path)
            ?.bufferedReader()
            ?.use { it.readText() }
            ?: error("migration resource not found: $path")
}
