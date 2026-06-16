package com.example.app.observability

import kotlinx.coroutines.ThreadContextElement
import kotlinx.coroutines.withContext
import org.slf4j.MDC
import kotlin.coroutines.CoroutineContext

/**
 * Propagates SLF4J MDC across coroutine suspensions.  Without this, a request
 * id put into MDC before `coAwait()` would be missing in the log line after
 * resumption because we may be on a different event-loop thread.
 *
 * Usage:
 *   withContext(MDCContext(mapOf("requestId" to id))) {
 *       handle(req)        // every log inside sees requestId
 *   }
 */
class MDCContext(
    private val state: Map<String, String> = MDC.getCopyOfContextMap() ?: emptyMap(),
) : ThreadContextElement<Map<String, String>?>, kotlin.coroutines.AbstractCoroutineContextElement(Key) {

    companion object Key : CoroutineContext.Key<MDCContext>

    override fun updateThreadContext(context: CoroutineContext): Map<String, String>? {
        val old = MDC.getCopyOfContextMap()
        MDC.setContextMap(state)
        return old
    }

    override fun restoreThreadContext(context: CoroutineContext, oldState: Map<String, String>?) {
        if (oldState == null) MDC.clear() else MDC.setContextMap(oldState)
    }
}

suspend inline fun <T> withMdc(map: Map<String, String>, crossinline block: suspend () -> T): T =
    withContext(MDCContext(map)) { block() }
