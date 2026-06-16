# Chapter 4 — Coroutines internals: continuations & state machines

> By the end of this chapter you will be able to (a) read the Kotlin
> bytecode of a `suspend` function, (b) explain what a continuation is in
> terms a debugger can show you, (c) draw the state machine the compiler
> emits.

If you are happy treating coroutines as a black box that lets you write
async code as if it were synchronous — you can skim this chapter. But
this is exactly what makes the Vert.x ↔ coroutine bridge cheap: there
is no thread per coroutine, just a tiny state machine. Knowing that
changes how you write code.

## 4.1 The naive model: "a coroutine is a fibre"

Wrong. People say "Kotlin coroutines are like green threads / fibres".
That is a useful first lie but it is misleading. There is no fibre. A
coroutine is a *function call*. A `suspend` function is a function that
can pause and resume — by returning a special value and being re-entered
later — but at any one moment it is just code on the call stack of
whatever thread is running it.

## 4.2 What `suspend` means

A `suspend` function is the same as a normal function with **one
implicit extra parameter**: a `Continuation<T>`. The compiler rewrites:

```kotlin
suspend fun greet(name: String): String {
    delay(100)
    return "hi, $name"
}
```

approximately into Java like:

```java
Object greet(String name, Continuation<? super String> $cont) { ... }
```

The return type changes from `String` to `Object` because at runtime
the function returns *either* the actual value (when it completes
synchronously) *or* a sentinel `COROUTINE_SUSPENDED` (when it suspends).

## 4.3 The state machine the compiler emits

Take a function with two suspension points:

```kotlin
suspend fun fetch(id: Long): User {
    val cached = redis.get(id)        // suspension 1
    if (cached != null) return cached.toUser()
    val u = pool.query(...).coAwait() // suspension 2
    return u
}
```

The compiler generates **one class** that captures locals (`id`,
`cached`, `u`) and **one giant switch** keyed on a `label` int. Each
`when` branch is the code between two suspension points. Pseudo-code:

```kotlin
final class FetchContinuation : Continuation<User> {
    int label;
    Long id;
    String cached;

    Object invokeSuspend(Object data) {
        switch (label) {
            case 0:
                this.id = …;                       // read args
                this.label = 1;
                Object r = redis.get(id, this);    // hand ourselves as cont
                if (r == COROUTINE_SUSPENDED) return COROUTINE_SUSPENDED;
                this.cached = (String) r;
                // fallthrough
            case 1:
                if (cached != null) return cached.toUser();
                this.label = 2;
                Object r = pool.query(...).coAwait(this);
                if (r == COROUTINE_SUSPENDED) return COROUTINE_SUSPENDED;
                return (User) r;
            case 2:
                // resumed; data is the awaited User
                return (User) data;
        }
    }
}
```

Reads bottom-to-top:

1. When the coroutine first runs, `label == 0`. We start the redis call,
   passing `this` as continuation. If redis is slow, we get back
   `COROUTINE_SUSPENDED`. The function returns immediately. The OS
   thread is freed.
2. Redis eventually has the value. It calls `cont.resumeWith(value)`.
   That re-enters `invokeSuspend` with `data = value` and `label = 1`.
3. We pick up where we left off, do the conditional, perhaps do another
   suspension at `label = 2`, and finally return.

**The continuation object is the coroutine.** It captures *just enough*
state to resume. Its size is roughly the sum of the locals that survive
across suspension points, plus a few bookkeeping fields. For a typical
`suspend` function: 64 to a few hundred bytes.

That is dramatically less than a thread stack (~512 KB by default).

## 4.4 Comparing to a virtual thread

A virtual thread parks its **whole stack** into a JDK-managed
`StackChunk`. The stack chunk is small (a few KB typically) but it
keeps every object the stack referenced alive. A continuation only
keeps the surviving locals. For very high-fanout workloads — chat,
sessions, sockets — that difference compounds.

We measure this in Chapter 19 but as a rough rule:

```
   1M concurrent virtual threads ≈ 2-4 GB heap (stack chunks + refs)
   1M concurrent coroutines      ≈ a few hundred MB (continuations)
```

You can do both. The point isn't "coroutines win"; it's "they use less".

## 4.5 The dispatcher

A `CoroutineDispatcher` answers one question: **on which thread should
I run a continuation when its result arrives?** Examples:

- `Dispatchers.Default`: a JVM-wide ForkJoinPool. Good for CPU work.
- `Dispatchers.IO`: a larger pool tuned for blocking I/O. Bad on Vert.x
  (you don't want to schedule continuations off the event loop).
- `Dispatchers.Unconfined`: resume on whatever thread completed the
  Future. Sounds good; in practice you lose Vert.x context.
- `Dispatchers.Vertx` (via `CoroutineVerticle.coroutineContext` or
  `vertx.dispatcher()`): resume on the originating Vert.x Context's
  event-loop thread. **This is what we use.**

Picking the wrong dispatcher is a common source of "why is this not
thread-confined?" bugs. Inside a `CoroutineVerticle`, the dispatcher is
correct by default. Inside a `runBlocking { }` at startup, you have to
remember to use `vertx.dispatcher()` when launching your verticle.

## 4.6 Inspecting it yourself

Take a tiny demo:

```kotlin
suspend fun demo() {
    println("A on ${Thread.currentThread().name}")
    delay(10)
    println("B on ${Thread.currentThread().name}")
}
```

Compile with `kotlinc -Xprint-bytecode demo.kt | less` or use
IntelliJ's "Show Kotlin Bytecode" → "Decompile". You will see the
generated continuation class.

For runtime inspection, add `-Dkotlinx.coroutines.debug` to your JVM
args. Now `coroutineContext[CoroutineName]` is populated and stack
traces show coroutine-aware frames.

## 4.7 Why this matters for the rest of the book

- The "magic" of `coAwait()` is **one** registered callback on a
  `Future` that calls `cont.resumeWith(...)`. Nothing else.
- `withContext(Dispatchers.IO) { … }` shifts which thread runs the
  resume. We use it sparingly.
- A continuation that is never resumed is a **memory leak**. Vert.x
  Futures complete or fail (always). If you write your own bridge,
  ensure both paths complete the continuation.
- Cancellation is just calling `cont.cancel(reason)`. It throws a
  `CancellationException` into the coroutine at the next suspension
  point. That is why structured concurrency works at all (Chapter 6).

## 4.8 Exercises

1. Write a `suspend fun add(a: Int, b: Int): Int { delay(1); return a+b }`
   and look at its bytecode. Find the `label` field and the
   `COROUTINE_SUSPENDED` constant.
2. Implement `suspend fun delayMillis(ms: Long)` using only Vert.x —
   no `kotlinx.coroutines.delay`. (Hint: `suspendCancellableCoroutine`
   and `vertx.setTimer`.)
3. Use `kotlinx.coroutines.debug.DebugProbes.install()` in a small test
   harness. Dump the running coroutines. What's surprising?

---

[← Chapter 3](03-futures.md) · [Next → Chapter 5: Coroutines + Vert.x, killing the callback](05-coroutines-vertx.md)
