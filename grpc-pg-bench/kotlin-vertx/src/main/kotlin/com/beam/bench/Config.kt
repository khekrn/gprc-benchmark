package com.beam.bench

/**
 * Runtime configuration sourced from environment variables. Values mirror the
 * Go server's env vars so both stacks read the same knobs from config.sh.
 */
data class Config(
    val pgHost: String,
    val pgPort: Int,
    val pgDb: String,
    val pgUser: String,
    val pgPassword: String,
    val pgPoolMax: Int,
    val pgPoolMin: Int,
    val listenHost: String,
    val listenPort: Int,
    val eventLoops: Int,
    val pipeliningLimit: Int,
) {
    companion object {
        fun fromEnv(): Config = Config(
            pgHost = env("PG_HOST", "127.0.0.1"),
            pgPort = envInt("PG_PORT", 5432),
            pgDb = env("PG_DB", "bench"),
            pgUser = env("PG_USER", "bench"),
            pgPassword = env("PG_PASSWORD", "bench"),
            pgPoolMax = envInt("PG_POOL_MAX", 16),
            pgPoolMin = envInt("PG_POOL_MIN", 4),
            listenHost = env("LISTEN_HOST", "127.0.0.1"),
            listenPort = envInt("LISTEN_PORT", 50052),
            eventLoops = envInt("VERTX_EVENT_LOOPS", 2),
            pipeliningLimit = envInt("PG_PIPELINING_LIMIT", 256),
        )

        private fun env(key: String, def: String): String =
            System.getenv(key)?.takeIf { it.isNotBlank() } ?: def

        private fun envInt(key: String, def: Int): Int =
            System.getenv(key)?.toIntOrNull() ?: def
    }
}
