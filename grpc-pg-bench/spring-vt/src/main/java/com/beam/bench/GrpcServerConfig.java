package com.beam.bench;

import io.grpc.netty.NettyServerBuilder;
import io.netty.channel.ChannelOption;
import io.netty.channel.EventLoopGroup;
import io.netty.channel.MultiThreadIoEventLoopGroup;
import io.netty.channel.epoll.Epoll;
import io.netty.channel.epoll.EpollIoHandler;
import io.netty.channel.epoll.EpollServerSocketChannel;
import io.netty.channel.nio.NioIoHandler;
import io.netty.channel.socket.nio.NioServerSocketChannel;
import jakarta.annotation.PreDestroy;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.grpc.server.ServerBuilderCustomizer;

import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/**
 * Tunes the grpc-netty server for this 2-core box. Two things matter here, both
 * justified by the CPU profile (≈72% of CPU was in the Netty/gRPC transport,
 * ≈57% in syscalls + cross-thread wakeups — see spring-vt/PROFILING.md):
 *
 *  1. <b>Run RPC handlers on virtual threads.</b> Without an explicit executor,
 *     grpc-java uses its default cached <i>platform</i> pool; setting the VT
 *     executor is what makes each handler — and the blocking JDBC inside it —
 *     park a virtual thread on I/O. (This was the original spring-vt behaviour.)
 *
 *  2. <b>Use the Linux epoll transport instead of NIO.</b> epoll avoids the NIO
 *     selector's per-loop syscall overhead and is the standard Linux gRPC tuning.
 *     Netty 4.2 deprecated {@code EpollEventLoopGroup}; the current API is
 *     {@link MultiThreadIoEventLoopGroup} + {@link EpollIoHandler}. We fall back
 *     to NIO automatically if epoll isn't available (e.g. on macOS).
 *
 * Plus small HTTP/2 knobs matching the go-pgx server: 1 MiB flow-control window,
 * TCP_NODELAY (gRPC writes are tiny), and server keepalive. The event-loop
 * threads do I/O only (handlers are off-loaded to VTs), so we size the worker
 * group to the available cores rather than Netty's default 2×cores.
 */
@Configuration
class GrpcServerConfig {

    private static final Logger log = LoggerFactory.getLogger(GrpcServerConfig.class);

    // Netty I/O (worker) event-loop thread count — the single source of truth is
    // application.properties (NETTY_IO_THREADS=1). Spring resolves this placeholder
    // from the OS environment FIRST and only then from application.properties, so
    // the perf A/B harness can still force 2 with `NETTY_IO_THREADS=2` in the env
    // without editing the file. 0/blank => auto = cores/2 (1 under the taskset pin).
    // This sizes the WORKER group only; the boss/accept group is always 1.
    @Value("${NETTY_IO_THREADS:0}")
    private int configuredIoThreads;

    private EventLoopGroup boss;
    private EventLoopGroup worker;

    @Bean
    ServerBuilderCustomizer<NettyServerBuilder> tunedNettyServerExecutor() {
        // I/O-thread count comes from application.properties (NETTY_IO_THREADS=1),
        // injected above; 0 means auto = cores/2. availableProcessors() honours the
        // taskset CPU affinity on Linux, so cores/2 is 1 on the 2-core-pinned box.
        // Why 1: the handlers run on virtual threads, so the Netty event loop only
        // does socket I/O + HTTP/2 framing. An A/B on the 2-core box showed 1 I/O
        // thread beats 2 by up to +14% (and cuts p99 ~30% at c=128). MECHANISM
        // (perf-stat A/B, results/perf-cohost2-*): it is NOT fewer context switches —
        // those stayed ~flat (~18k/s, ~6-7 per million instructions) whether 1 or 2
        // I/O threads, because ctx switches are driven by virtual-thread park/unpark
        // on the blocking JDBC round-trips, not by the event loops. The real cost of
        // a 2nd I/O thread is CACHE LOCALITY: two event loops split across cores 2,3
        // bounce connection/buffer state between the two cores' caches → IPC -6%
        // (0.483→0.454) and cache-misses +10%. On this DB-bound path rps looks
        // identical (Postgres is the wall), but 1 I/O thread is the more CPU-efficient
        // choice. cores/2 → 1 on 2 cores, 2 on 4 cores.
        int ioThreads = configuredIoThreads > 0
                ? configuredIoThreads
                : Math.max(1, Runtime.getRuntime().availableProcessors() / 2);
        // NETTY_TRANSPORT=nio forces NIO even on Linux — lets us A/B the same
        // binary (epoll vs nio) back-to-back in one session to control for
        // day-to-day box variance. Default (unset/"epoll") uses epoll if available.
        boolean forceNio = "nio".equalsIgnoreCase(System.getenv("NETTY_TRANSPORT"));
        boolean epoll = !forceNio && Epoll.isAvailable();
        if (epoll) {
            boss = new MultiThreadIoEventLoopGroup(1, EpollIoHandler.newFactory());
            worker = new MultiThreadIoEventLoopGroup(ioThreads, EpollIoHandler.newFactory());
        } else {
            boss = new MultiThreadIoEventLoopGroup(1, NioIoHandler.newFactory());
            worker = new MultiThreadIoEventLoopGroup(ioThreads, NioIoHandler.newFactory());
        }
        log.info("grpc-netty transport: {} (io threads={})", epoll ? "epoll" : "nio", ioThreads);

        final EventLoopGroup bossRef = boss;
        final EventLoopGroup workerRef = worker;
        return builder -> builder
                .executor(Executors.newVirtualThreadPerTaskExecutor())
                .channelType(epoll ? EpollServerSocketChannel.class : NioServerSocketChannel.class)
                .bossEventLoopGroup(bossRef)
                .workerEventLoopGroup(workerRef)
                .withChildOption(ChannelOption.TCP_NODELAY, true)
                .flowControlWindow(1 << 20)
                .maxConcurrentCallsPerConnection(Integer.MAX_VALUE)
                .keepAliveTime(30, TimeUnit.SECONDS)
                .keepAliveTimeout(10, TimeUnit.SECONDS)
                .permitKeepAliveWithoutCalls(true);
    }

    @PreDestroy
    void shutdownEventLoops() {
        if (worker != null) {
            worker.shutdownGracefully();
        }
        if (boss != null) {
            boss.shutdownGracefully();
        }
    }
}
