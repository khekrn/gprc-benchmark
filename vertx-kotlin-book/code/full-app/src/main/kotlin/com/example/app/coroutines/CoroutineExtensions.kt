package com.example.app.coroutines

import io.vertx.core.Future
import io.vertx.core.Promise
import io.vertx.core.streams.ReadStream
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * Helpers that bridge Vert.x async types ↔ kotlinx coroutines.  Most apps
 * only need `coAwait()` from vertx-lang-kotlin-coroutines; everything below
 * is for the few cases the bridge does not cover.
 */

/**
 * Adapt a Vert.x [ReadStream] into a cold, back-pressured coroutine [Flow].
 *
 * The stream starts paused; we prime it with [capacity] elements and then
 * pull exactly one more for every element the collector drains via
 * `fetch(1)`.  Because the stream only ever delivers what we have asked for,
 * the bounded channel never overflows and back-pressure flows all the way
 * upstream — no element is dropped and the producer is never ahead of the
 * collector by more than [capacity].
 */
fun <T> ReadStream<T>.asFlow(capacity: Int = 16): Flow<T> {
    val stream = this
    return flow {
        val channel = Channel<T>(capacity)
        stream.handler { item -> channel.trySend(item) }
        stream.endHandler { channel.close() }
        stream.exceptionHandler { t -> channel.close(t) }
        stream.pause()
        stream.fetch(capacity.toLong())
        for (item in channel) {
            emit(item)
            stream.fetch(1)   // request one more now that we've handed one off
        }
    }
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
suspend fun <T> awaitFuture(provider: (Promise<T>) -> Unit): T =
    suspendCancellableCoroutine { cont: CancellableContinuation<T> ->
        val p = Promise.promise<T>()
        provider(p)
        p.future().onComplete { ar ->
            if (ar.succeeded()) cont.resume(ar.result())
            else cont.resumeWithException(ar.cause())
        }
    }
