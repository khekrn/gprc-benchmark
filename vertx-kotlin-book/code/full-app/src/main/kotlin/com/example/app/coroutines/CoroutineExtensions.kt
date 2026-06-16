package com.example.app.coroutines

import io.vertx.core.Future
import io.vertx.core.streams.ReadStream
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.consumeAsFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * Helpers that bridge Vert.x async types ↔ kotlinx coroutines.  Most apps
 * only need `coAwait()` from vertx-lang-kotlin-coroutines; everything below
 * is for the few cases the bridge does not cover.
 */

/**
 * Adapt a Vert.x ReadStream into a coroutine Flow with back-pressure.
 * Each downstream collector "pull" lets the upstream stream produce more
 * items.  Capacity controls the maximum in-flight buffered items.
 */
fun <T> ReadStream<T>.asFlow(capacity: Int = Channel.BUFFERED): Flow<T> {
    val channel = Channel<T>(capacity)
    handler { item ->
        val result = channel.trySend(item)
        if (!result.isSuccess) pause()      // back-pressure: stop upstream
    }
    exceptionHandler { t -> channel.close(t) }
    endHandler { channel.close() }
    channel.invokeOnClose { /* upstream stream will be closed by handlers above */ }
    return channel.consumeAsFlow()
}

/**
 * Suspend until a Vert.x Future completes — same as `coAwait()` but
 * exposed as `await()` to match kotlinx.coroutines idiom.
 */
suspend fun <T> Future<T>.await(): T = coAwait()

/**
 * Suspending equivalent of `Promise.future().onComplete(...)` used in
 * "bridge" code that is not already on a coroutine context.
 */
suspend fun <T> awaitFuture(provider: (io.vertx.core.Promise<T>) -> Unit): T =
    suspendCancellableCoroutine { cont: CancellableContinuation<T> ->
        val p = io.vertx.core.Promise.promise<T>()
        provider(p)
        p.future().onComplete { ar ->
            if (ar.succeeded()) cont.resume(ar.result())
            else cont.resumeWithException(ar.cause())
        }
    }
