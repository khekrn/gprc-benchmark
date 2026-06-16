# Chapter 3 — Futures, Promises, and the Vert.x async type

> By the end of this chapter you will know exactly what a `Future<T>` is
> in Vert.x 5, how to compose without callbacks using `compose` /
> `andThen` / `vertxFuture { … }`, when to reach for `Promise`, and how
> they bridge to coroutines via `coAwait`.

The Vert.x async type is **`io.vertx.core.Future<T>`**. Every Vert.x API
that returns a value (HTTP listen, DB query, file read) returns a
`Future<T>`. It is the *type-level* equivalent of "this will be available
later".

## 3.1 Three properties to remember

1. **A `Future<T>` is event-loop affine.** All its handlers run on the
   Context the Future was created on. That is the whole reason a `coAwait`
   resumes back on your event loop.
2. **It is `OnCompletion-once`.** A Future completes exactly once. Adding
   a handler after completion fires the handler immediately.
3. **It is *cold-ish*.** Until you await or attach a handler, no
   computation is forced — but it has *already started* the underlying
   I/O. Unlike Reactor `Mono`, Vert.x futures are eager.

## 3.2 Building a Future: the `vertxFuture { … }` builder

Vert.x 5 ships a modern coroutine builder, `vertxFuture { … }`, that
lets you write a suspending block and get back a `Future<T>`. This is
the cleanest way to expose suspending code to non-coroutine callers (like
a gRPC generated interface that wants `Future<UserReply>`).

```kotlin
import io.vertx.kotlin.coroutines.vertxFuture

fun getUser(id: Long): Future<UserReply> = vertxFuture {
    val u = users.getById(id)          // suspending
    u.toReply()
}
```

Under the hood `vertxFuture { … }` launches a coroutine on
`Dispatchers.Vertx` (the current Vert.x Context), runs the block, and
completes the returned Future with the value or the thrown exception.

You will see us use this in gRPC code where the *generator* dictates a
`Future`-returning shape but we want to write straight-line code
inside.

## 3.3 The legacy way: `Promise`

A `Promise<T>` is the write-side of a Future. You complete or fail it;
others observe it via its `future()`.

```kotlin
fun loadAsync(): Future<String> {
    val p = Promise.promise<String>()
    vertx.fileSystem().readFile("/data/a.txt").onComplete { ar ->
        if (ar.succeeded()) p.complete(ar.result().toString())
        else                p.fail(ar.cause())
    }
    return p.future()
}
```

You usually don't need `Promise`. Prefer:

- `vertxFuture { … }` if your body is suspending,
- `Future.future { p -> p.complete(…) }` for one-off shorthand,
- chaining built-ins instead of writing your own.

## 3.4 Composition without callbacks

`Future` has the usual combinators. We list them with the equivalent
coroutine code for reference.

| Future                                   | Coroutine                                                     |
|------------------------------------------|---------------------------------------------------------------|
| `f.map { x -> g(x) }`                    | `g(f.coAwait())`                                              |
| `f.compose { x -> h(x) }`                | `h(f.coAwait()).coAwait()`                                    |
| `f.recover { e -> Future.succeededFuture("fallback") }` | `runCatching { f.coAwait() }.getOrElse { "fallback" }` |
| `f.andThen { ar -> log(ar) }`            | `try { f.coAwait().also { log(it) } } catch (e: …) { log(e); throw e }` |
| `Future.all(a, b, c)`                    | `awaitAll(a, b, c)` (after `coAwait` on each) — see ch 5      |

The chained `Future` style works and is what you must read in stack
traces of any Vert.x library. The coroutine style is what we *write* in
this book. They interoperate cleanly through `.coAwait()` and
`vertxFuture { }`.

## 3.5 Example: a real combinator chain rewritten

Callback / chained style:

```kotlin
fun createIfMissing(input: NewUser): Future<User> =
    repo.findByEmail(input.email).compose { existing ->
        if (existing != null) Future.succeededFuture(existing)
        else                 repo.create(input)
    }
```

Coroutine style with `vertxFuture`:

```kotlin
fun createIfMissing(input: NewUser): Future<User> = vertxFuture {
    repo.findByEmail(input.email) ?: repo.create(input)
}
```

Both compile to the same runtime work. The second one reads top-to-bottom
and you can step through it in a debugger like normal code.

## 3.6 Concurrency: `Future.all`, `Future.any`, `Future.join`

`Future.all(a, b, c)` succeeds when all complete, fails if any fail.
Useful for "fan out two queries in parallel, then continue".

```kotlin
val a = repo.countActive()
val b = repo.countTotal()
Future.all(a, b).onSuccess { cf ->
    val active = cf.resultAt<Long>(0)
    val total  = cf.resultAt<Long>(1)
    // ...
}
```

Coroutine equivalent (with structured concurrency, Chapter 6):

```kotlin
coroutineScope {
    val active = async { repo.countActive() }
    val total  = async { repo.countTotal()  }
    val pair   = active.await() to total.await()
}
```

For most apps the coroutine version is easier to reason about.

## 3.7 Error handling

A `Future` that fails carries a `Throwable`. Handlers downstream see it
as `ar.failed() / ar.cause()` or, in `.coAwait()`, **as a thrown
exception**. That is the magic: a coroutine throws normally, you
`try/catch` normally, and the failure flows up.

```kotlin
try {
    val u = users.getById(id)        // throws UserError.NotFound
    return u
} catch (e: UserError.NotFound) {
    writeProblem(ctx, 404, "Not Found", e.message)
}
```

Compare to the chained form:

```kotlin
users.getById(id)
    .onSuccess { u -> ctx.response().end(toJson(u)) }
    .onFailure { e ->
        if (e is UserError.NotFound) writeProblem(ctx, 404, "Not Found", e.message)
        else                         writeProblem(ctx, 500, "Internal", null)
    }
```

You can write either; we write the first.

## 3.8 Common bug: forgetting to return the Future

```kotlin
fun badRoute(ctx: RoutingContext) {
    repo.findById(1)                         // Future created and ignored
        .onSuccess { ctx.response().end(it.toString()) }
    // function returns; future fires later; if it fails, you lose the error
}
```

If you don't *return* a Future, Vert.x cannot apply its error handlers
to it. The verticle's parent coroutine cannot wait for it. The Future
"escapes". The common symptom is a missing log line — the failure was
silently swallowed.

Coroutines fix this naturally: in a `suspend` function, **forgetting to
`.await` a `Future` is forgetting to use its value**, which is usually
a compile-time hint or a code-smell at review.

## 3.9 Future ↔ coroutine bridge cheat-sheet

```kotlin
// Future -> coroutine
val u: User = repo.findById(1).coAwait()

// coroutine -> Future
fun get(id: Long): Future<User?> = vertxFuture {
    repo.findById(id)
}

// suspend lambda -> Future (manual)
fun get(id: Long): Future<User?> {
    val p = Promise.promise<User?>()
    scope.launch {
        try { p.complete(repo.findById(id)) } catch (e: Throwable) { p.fail(e) }
    }
    return p.future()
}
```

Prefer `vertxFuture { }`. Use the manual form only when you need to
control the scope explicitly (e.g. the coroutine should run with a
particular `MDCContext`).

## 3.10 Exercises

1. Add a route that returns *both* `countTotal` and `countActive` as
   one JSON response, using `vertxFuture { coroutineScope { … } }` and
   `async`. Measure latency.
2. Take the same route and rewrite it with `Future.all(a, b)`. Which
   reads better to you? Which produces clearer stack traces if `a`
   fails?
3. In `UserGrpcService.kt`, find `runOnScope { … }` and rewrite it
   using `vertxFuture { }` directly.

---

[← Chapter 2](02-verticles.md) · [Next → Chapter 4: Coroutines internals](04-coroutines-internals.md)
