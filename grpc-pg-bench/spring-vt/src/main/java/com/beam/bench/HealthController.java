package com.beam.bench;

import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Minimal REST liveness endpoint served by the Jetty container that now runs
 * alongside the grpc-netty server in the same JVM.
 *
 * <p>The body does <b>no work</b> — no DB call, no allocation beyond the constant
 * string — on purpose. This experiment measures the cost of co-hosting a second
 * network stack (Jetty + its acceptor/selector/handler threads) next to gRPC on
 * a 2-core box, so the probe itself must be near-free: any latency or jitter we
 * see on {@code /health} while the gRPC benchmark hammers the box is attributable
 * to contention (CPU, event loops, GC, cache) between the two servers, not to the
 * handler doing something. A health checker pings this every 5s from outside the
 * benchmark script (see {@code scripts/health_ping.sh}).
 *
 * <p>Returns a plain {@code 200 "UP"} rather than going through Spring Boot
 * Actuator: Actuator's {@code /actuator/health} aggregates a {@code DataSource}
 * check that borrows a HikariCP connection per call, which would couple the probe
 * to DB-pool contention and muddy the co-host signal.
 */
@RestController
public class HealthController {

    @GetMapping(value = "/health", produces = MediaType.TEXT_PLAIN_VALUE)
    public String health() {
        return "UP";
    }
}
