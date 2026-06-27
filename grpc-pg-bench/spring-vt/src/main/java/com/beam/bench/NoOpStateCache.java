package com.beam.bench;

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Component;

/**
 * Default {@link StateCache}: no caching, every {@link Db#getState} hits Postgres.
 * Active whenever {@code bench.redis.enabled} is false or unset, so the original
 * spring-vt baseline is preserved unless Redis is explicitly turned on. Paired
 * with {@link RedisStateCache} (active when the flag is true) via mutually
 * exclusive {@code @ConditionalOnProperty} so exactly one bean exists.
 */
@Component
@ConditionalOnProperty(name = "bench.redis.enabled", havingValue = "false", matchIfMissing = true)
class NoOpStateCache implements StateCache {

    @Override
    public Db.StateRow get(String workflowId) {
        return null; // always a miss → Db reads Postgres
    }

    @Override
    public void put(String workflowId, Db.StateRow row) {
        // no-op
    }

    @Override
    public void invalidate(String workflowId) {
        // no-op
    }
}
