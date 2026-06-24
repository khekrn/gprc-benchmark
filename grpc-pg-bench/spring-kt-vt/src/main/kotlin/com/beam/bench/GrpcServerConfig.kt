package com.beam.bench

import io.grpc.netty.NettyServerBuilder
import io.netty.channel.ChannelOption
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.grpc.server.ServerBuilderCustomizer
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * Forces the gRPC server's call executor onto virtual threads AND tunes the
 * Netty transport for high concurrency (c=128).
 *
 * `spring.threads.virtual.enabled=true` switches Spring's own task executors to
 * virtual threads, but the gRPC server otherwise falls back to grpc-java's
 * default cached *platform* thread pool (the "grpc-default-executor" threads).
 * Setting the executor explicitly is what actually makes each RPC handler — and
 * therefore the blocking JDBC call inside it — run on a virtual thread, which
 * is the whole point of this stack (see CLAUDE.md spring-vt VT-executor gotcha).
 *
 * The transport knobs mirror the intent of the go-pgx server's gRPC options:
 * 1 MiB HTTP/2 flow-control windows (so flow control doesn't serialize short
 * RPCs at high concurrency), generous concurrent-call limit, server keepalive,
 * and TCP_NODELAY to avoid Nagle latency on small frames.
 *
 * No Spring Security on the classpath, so no DelegatingSecurityContextExecutor
 * wrapping is required.
 */
@Configuration
class GrpcServerConfig {

    @Bean
    fun virtualThreadServerExecutor(): ServerBuilderCustomizer<NettyServerBuilder> =
        ServerBuilderCustomizer { builder ->
            builder
                // Each handler (and its blocking JDBC) on a fresh virtual thread.
                .executor(Executors.newVirtualThreadPerTaskExecutor())
                // 1 MiB HTTP/2 windows — defaults (64 KiB) throttle short-RPC
                // throughput at high concurrency waiting on WINDOW_UPDATE frames.
                .initialFlowControlWindow(1 shl 20)
                .flowControlWindow(1 shl 20)
                // Don't cap concurrent streams per connection; the client opens
                // many in-flight unary calls at c=128.
                .maxConcurrentCallsPerConnection(Int.MAX_VALUE)
                // Server keepalive: ping idle connections, fail fast on dead peers.
                .keepAliveTime(30, TimeUnit.SECONDS)
                .keepAliveTimeout(10, TimeUnit.SECONDS)
                .permitKeepAliveWithoutCalls(true)
                .permitKeepAliveTime(10, TimeUnit.SECONDS)
                // Disable Nagle: send small response frames immediately.
                .withChildOption(ChannelOption.TCP_NODELAY, true)
        }
}
