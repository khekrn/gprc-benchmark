package com.beam.bench;

import io.lettuce.core.resource.ClientResources;
import io.lettuce.core.resource.DefaultClientResources;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

/**
 * Lettuce/Netty resource tuning for the 2-core t4g.medium target, applied only
 * when Redis is enabled ({@code bench.redis.enabled=true}). Providing a
 * {@link ClientResources} bean overrides Boot's default (which sizes Lettuce's
 * netty I/O + computation event-loop groups to {@code max(3, availableProcessors)}
 * — that would be the host's full core count if the JVM isn't CPU-pinned).
 *
 * <p>On 2 cores we pin both pools to Lettuce's floor of 3, so the Redis client adds
 * a known, small thread footprint (3 I/O + 3 computation) next to grpc-netty's 1
 * I/O thread and the virtual-thread carriers — rather than letting it scale with
 * the box. Connection model (single shared connection vs a commons-pool2 pool) and
 * the command timeout are driven from {@code application.properties}
 * ({@code spring.data.redis.*}); see there.
 */
@Configuration
@ConditionalOnProperty(name = "bench.redis.enabled", havingValue = "true")
class RedisConfig {

    private static final Logger log = LoggerFactory.getLogger(RedisConfig.class);

    @Bean(destroyMethod = "shutdown")
    ClientResources lettuceClientResources(
            @Value("${bench.redis.io-threads:3}") int ioThreads,
            @Value("${bench.redis.computation-threads:3}") int computationThreads) {
        log.info("Lettuce ClientResources: io-threads={}, computation-threads={}",
                ioThreads, computationThreads);
        return DefaultClientResources.builder()
                .ioThreadPoolSize(ioThreads)
                .computationThreadPoolSize(computationThreads)
                .build();
    }
}
