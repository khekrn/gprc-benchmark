package com.beam.bench;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Redis-backed read-through cache for {@code workflow_state}, using Lettuce via
 * Spring's {@link StringRedisTemplate} with SYNCHRONOUS commands. Each call runs
 * on the request's virtual thread, so the blocking GET/SET parks the carrier on
 * the Redis socket — the same Loom model spring-vt already uses for JDBC.
 *
 * <p><b>Value encoding</b> is a compact {@code version|updatedAtMicros|state}
 * string (state last, so it may itself contain the delimiter) — cheaper than JSON
 * for a 3-field row and the only fields {@link Db.StateRow} needs to reconstruct.
 *
 * <p><b>Resilience:</b> every Redis op is wrapped so a Redis outage degrades to
 * Postgres rather than failing the RPC — {@link #get} returns {@code null} (miss)
 * on error, {@link #put}/{@link #invalidate} swallow it. Errors are logged at WARN
 * but throttled to one line/second so a hard Redis outage can't flood the log.
 * (TTL is the backstop for a missed invalidate: bounded staleness.)
 */
@Component
@ConditionalOnProperty(name = "bench.redis.enabled", havingValue = "true")
class RedisStateCache implements StateCache {

    private static final Logger log = LoggerFactory.getLogger(RedisStateCache.class);
    private static final String KEY_PREFIX = "wf:state:";

    private final StringRedisTemplate redis;
    private final Duration ttl;
    private final AtomicLong lastWarnNanos = new AtomicLong(0);

    RedisStateCache(StringRedisTemplate redis,
                    @Value("${bench.redis.ttl-seconds:300}") long ttlSeconds) {
        this.redis = redis;
        this.ttl = Duration.ofSeconds(ttlSeconds);
        log.info("Redis read-through cache ENABLED (ttl={}s)", ttlSeconds);
    }

    private static String key(String workflowId) {
        return KEY_PREFIX + workflowId;
    }

    @Override
    public Db.StateRow get(String workflowId) {
        try {
            String v = redis.opsForValue().get(key(workflowId));
            if (v == null) {
                return null;
            }
            int p1 = v.indexOf('|');
            int p2 = v.indexOf('|', p1 + 1);
            if (p1 < 0 || p2 < 0) {
                return null; // malformed → treat as miss
            }
            long version = Long.parseLong(v.substring(0, p1));
            long micros = Long.parseLong(v.substring(p1 + 1, p2));
            String state = v.substring(p2 + 1);
            return new Db.StateRow(true, workflowId, state, version, micros);
        } catch (RuntimeException e) {
            warn("get", e);
            return null; // degrade to Postgres
        }
    }

    @Override
    public void put(String workflowId, Db.StateRow row) {
        try {
            String value = row.version() + "|" + row.updatedAtMicros() + "|" + row.state();
            redis.opsForValue().set(key(workflowId), value, ttl);
        } catch (RuntimeException e) {
            warn("put", e);
        }
    }

    @Override
    public void invalidate(String workflowId) {
        try {
            redis.delete(key(workflowId));
        } catch (RuntimeException e) {
            warn("invalidate", e);
        }
    }

    /** Log at most one WARN/second so a Redis outage can't flood the log. */
    private void warn(String op, RuntimeException e) {
        long now = System.nanoTime();
        long prev = lastWarnNanos.get();
        if (now - prev > TimeUnit.SECONDS.toNanos(1) && lastWarnNanos.compareAndSet(prev, now)) {
            log.warn("Redis {} failed, degrading to Postgres: {}", op, e.toString());
        }
    }
}
