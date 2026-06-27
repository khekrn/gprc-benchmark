package com.beam.bench;

/**
 * Read-through cache for {@code workflow_state}, sitting in front of the Postgres
 * read in {@link Db#getState}. Two implementations, selected by the
 * {@code bench.redis.enabled} flag (env {@code REDIS_ENABLED}):
 *
 * <ul>
 *   <li>{@link RedisStateCache} — Lettuce/Redis, synchronous commands on the
 *       calling virtual thread.</li>
 *   <li>{@link NoOpStateCache} — does nothing (every read goes straight to PG);
 *       this is the default, so the original spring-vt behaviour is unchanged
 *       unless Redis is explicitly enabled.</li>
 * </ul>
 *
 * The cache is deliberately resilient: a Redis failure must degrade to Postgres,
 * never fail the RPC (see {@link RedisStateCache}).
 */
interface StateCache {

    /** Cache lookup. Returns {@code null} on a miss (or any Redis error). */
    Db.StateRow get(String workflowId);

    /** Populate the cache after a Postgres read of an existing row. */
    void put(String workflowId, Db.StateRow row);

    /** Drop the cached entry after a write (ExecuteTx upserts workflow_state). */
    void invalidate(String workflowId);
}
