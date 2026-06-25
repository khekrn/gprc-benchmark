package com.beam.bench

import io.grpc.netty.NettyServerBuilder
import io.netty.channel.ChannelOption
import io.netty.channel.EventLoopGroup
import io.netty.channel.MultiThreadIoEventLoopGroup
import io.netty.channel.epoll.Epoll
import io.netty.channel.epoll.EpollIoHandler
import io.netty.channel.epoll.EpollServerSocketChannel
import io.netty.channel.nio.NioIoHandler
import io.netty.channel.socket.nio.NioServerSocketChannel
import jakarta.annotation.PreDestroy
import org.slf4j.LoggerFactory
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.grpc.server.ServerBuilderCustomizer
import java.util.concurrent.Executor
import java.util.concurrent.TimeUnit

/**
 * Netty tuning for the **reactive** spring-rt server on the 2-core box. The
 * baseline wall-clock profile showed ~96% of time in syscalls + cross-thread
 * wakeups (`pthread_cond_signal`/`__lll_lock_wait`) — i.e. the cost is thread
 * hops and per-loop syscalls, NOT compute. So we cut both:
 *
 *  1. **Run handlers inline on the Netty event loop (direct executor).** Unlike
 *     spring-vt — which sets a *virtual-thread* executor so blocking JDBC can
 *     park a carrier — spring-rt is fully non-blocking (coroutines + R2DBC), so
 *     a VT executor would be exactly wrong: it would add a pointless thread hop
 *     per call. A **direct executor** instead starts each coroutine on the I/O
 *     thread that decoded the request; it only leaves that thread when it
 *     suspends on R2DBC (no work is blocked on the loop). This removes the
 *     hand-off to grpc-java's default platform pool — one fewer wakeup per RPC.
 *     SAFE ONLY because nothing on this path blocks (verified via `wall` profile).
 *
 *  2. **Linux epoll instead of NIO.** Fewer selector syscalls per loop than the
 *     NIO selector; the standard Linux gRPC tuning. Falls back to NIO if epoll
 *     is unavailable.
 *
 * Plus the same HTTP/2 knobs as go-pgx / spring-vt: 1 MiB flow-control window,
 * TCP_NODELAY (tiny gRPC writes), server keepalive. I/O threads default to the
 * visible core count (handlers run ON the loop here, so unlike spring-vt we do
 * NOT shrink to cores/2); override with NETTY_IO_THREADS to A/B.
 */
@Configuration
class GrpcServerConfig {

    private val log = LoggerFactory.getLogger(GrpcServerConfig::class.java)

    private var boss: EventLoopGroup? = null
    private var worker: EventLoopGroup? = null

    /** Executor that runs each task inline on the caller (the Netty I/O thread). */
    private val directExecutor = Executor { it.run() }

    @Bean
    fun reactiveNettyServerCustomizer(): ServerBuilderCustomizer<NettyServerBuilder> {
        // GRPC_TUNED=off returns the builder untouched (default NIO transport +
        // grpc-java's default platform executor) — the un-tuned baseline, for a
        // rigorous same-build A/B against the tuned path.
        if ("off".equals(System.getenv("GRPC_TUNED"), ignoreCase = true)) {
            log.info("grpc-netty: tuning DISABLED (GRPC_TUNED=off) — default NIO + default executor")
            return ServerBuilderCustomizer { }
        }

        // Handlers run ON the event loop (direct executor) only until they suspend
        // on R2DBC, so — like spring-vt — fewer loops means fewer cross-thread
        // wakeups. Default to cores/2 (=1 on the 2-core box); the A/B sweep showed
        // pool>32 and extra I/O threads add contention, not throughput.
        val ioEnv = System.getenv("NETTY_IO_THREADS")
        val ioThreads = if (!ioEnv.isNullOrBlank()) ioEnv.trim().toInt()
        else maxOf(1, Runtime.getRuntime().availableProcessors() / 2)

        val forceNio = "nio".equals(System.getenv("NETTY_TRANSPORT"), ignoreCase = true)
        val epoll = !forceNio && Epoll.isAvailable()
        if (epoll) {
            boss = MultiThreadIoEventLoopGroup(1, EpollIoHandler.newFactory())
            worker = MultiThreadIoEventLoopGroup(ioThreads, EpollIoHandler.newFactory())
        } else {
            boss = MultiThreadIoEventLoopGroup(1, NioIoHandler.newFactory())
            worker = MultiThreadIoEventLoopGroup(ioThreads, NioIoHandler.newFactory())
        }
        log.info("grpc-netty transport: {} (io threads={}, direct executor)", if (epoll) "epoll" else "nio", ioThreads)

        val bossRef = boss!!
        val workerRef = worker!!
        return ServerBuilderCustomizer { builder ->
            builder
                .executor(directExecutor)
                .channelType(if (epoll) EpollServerSocketChannel::class.java else NioServerSocketChannel::class.java)
                .bossEventLoopGroup(bossRef)
                .workerEventLoopGroup(workerRef)
                .withChildOption(ChannelOption.TCP_NODELAY, true)
                .flowControlWindow(1 shl 20)
                .maxConcurrentCallsPerConnection(Int.MAX_VALUE)
                .keepAliveTime(30, TimeUnit.SECONDS)
                .keepAliveTimeout(10, TimeUnit.SECONDS)
                .permitKeepAliveWithoutCalls(true)
        }
    }

    @PreDestroy
    fun shutdownEventLoops() {
        worker?.shutdownGracefully()
        boss?.shutdownGracefully()
    }
}
