# Chapter 16 — Building an Async DynamoDB Driver

> **You should finish this chapter able to:** design a Vert.x-native async
> driver for a non-trivial protocol; implement SigV4 signing; manage an
> HTTP/2 connection pool; expose typed Future-based APIs; and apply the same
> pattern to any AWS service you need.

DynamoDB's wire protocol is HTTP/1.1 + JSON over HTTPS, with SigV4
authentication. The official AWS Java SDK is excellent — but it's
*synchronous-by-default* in JDBC-style; its async client uses Netty too, but
not in a way you can compose with Vert.x's event loop. Building a thin
Vert.x-native driver gives us:

1. **Zero thread hops** between gRPC/HTTP requests and DynamoDB calls.
2. **Pool sharing** with the rest of your Vert.x app (same `Vertx` instance,
   same selectors, same Netty allocator).
3. **Backpressure** via `Future` chains and `coAwait`.
4. **A great learning project** that exercises chapters 1-15.

This chapter walks through the design. Treat it as a recipe you can adapt
to any AWS-style service (S3, SQS, Kinesis, ...).

## 16.1  What we're building

```
Your code
   │
   │ ddb.getItem(GetItemRequest("users", mapOf("id" to "u-42")))
   ▼
DynamoDbClient (suspend API)
   │
   ▼
RequestSigner (SigV4)
   │
   ▼
WebClient (Vert.x HTTP client)
   │
   ▼
TLS → DynamoDB endpoint (dynamodb.us-east-1.amazonaws.com)
```

We will keep DynamoDB-specific details (the JSON format) at one layer and
the protocol-level transport at another. The driver should:

- Be a Vert.x verticle-friendly client (a `Vertx`-bound, sharable object).
- Expose typed suspending methods: `getItem`, `putItem`, `query`, `scan`,
  `batchGetItem`, `transactWriteItems`.
- Handle retries with exponential backoff + jitter.
- Reuse HTTP connections via Vert.x's connection pool.
- Sign requests with AWS SigV4 (HMAC-SHA256).
- Surface DynamoDB errors as typed exceptions.

## 16.2  Project layout

```
src/main/kotlin/com/example/ddb/
  DynamoDbClient.kt                  ← public suspend API
  DynamoDbOptions.kt                 ← configuration
  internal/
    Signer.kt                        ← SigV4 implementation
    HttpTransport.kt                 ← thin WebClient wrapper
    JsonShapes.kt                    ← TypeAdapter for AttributeValue, etc.
    Retry.kt                         ← exponential backoff + jitter
    Errors.kt                        ← exception hierarchy
  shapes/
    AttributeValue.kt                ← Kotlin data class wrappers
    GetItemRequest.kt
    GetItemResponse.kt
    ...
```

## 16.3  The public API

```kotlin
class DynamoDbClient private constructor(
    private val transport: HttpTransport,
    private val signer: Signer,
    private val retry: Retry,
) {
    suspend fun getItem(req: GetItemRequest): GetItemResponse =
        invoke("DynamoDB_20120810.GetItem", req, GetItemResponse::class)

    suspend fun putItem(req: PutItemRequest): PutItemResponse =
        invoke("DynamoDB_20120810.PutItem", req, PutItemResponse::class)

    suspend fun query(req: QueryRequest): QueryResponse =
        invoke("DynamoDB_20120810.Query", req, QueryResponse::class)

    suspend fun batchGetItem(req: BatchGetItemRequest): BatchGetItemResponse =
        invoke("DynamoDB_20120810.BatchGetItem", req, BatchGetItemResponse::class)

    private suspend fun <Req : Any, Res : Any> invoke(
        target: String, req: Req, resType: KClass<Res>,
    ): Res = retry.execute {
        val bodyJson = JsonShapes.toJson(req)
        val signed = signer.sign(target, bodyJson)
        val resp = transport.post(signed).coAwait()
        when {
            resp.statusCode() == 200 -> JsonShapes.fromJson(resp.bodyAsString(), resType)
            resp.statusCode() in 400..499 -> throw decodeClientError(resp)
            else -> throw DynamoDbException("server error", resp.statusCode())
        }
    }

    companion object {
        fun create(vertx: Vertx, options: DynamoDbOptions): DynamoDbClient {
            val transport = HttpTransport(vertx, options)
            val signer = Signer(options.credentials, options.region, "dynamodb")
            val retry = Retry(maxAttempts = options.maxAttempts, baseDelayMs = 50)
            return DynamoDbClient(transport, signer, retry)
        }
    }
}
```

## 16.4  Options

```kotlin
data class DynamoDbOptions(
    val region: String,                          // us-east-1
    val endpoint: String? = null,                // override for local DynamoDB
    val credentials: AwsCredentialsProvider,
    val maxConnections: Int = 32,
    val keepAliveSeconds: Int = 60,
    val maxAttempts: Int = 4,
    val connectTimeoutMs: Long = 1000,
    val requestTimeoutMs: Long = 3000,
) {
    fun resolvedEndpoint(): String =
        endpoint ?: "https://dynamodb.$region.amazonaws.com"
}

interface AwsCredentialsProvider {
    suspend fun resolve(): AwsCredentials
}

data class AwsCredentials(val accessKeyId: String, val secretKey: String, val sessionToken: String? = null)
```

A `CredentialsProvider` is async because IAM-role / IMDSv2 / SSO lookups
require network calls. Default providers:

```kotlin
class StaticCredentialsProvider(private val c: AwsCredentials) : AwsCredentialsProvider {
    override suspend fun resolve() = c
}

class EnvCredentialsProvider : AwsCredentialsProvider {
    override suspend fun resolve() = AwsCredentials(
        accessKeyId = System.getenv("AWS_ACCESS_KEY_ID"),
        secretKey   = System.getenv("AWS_SECRET_ACCESS_KEY"),
        sessionToken = System.getenv("AWS_SESSION_TOKEN"),
    )
}

class Imdsv2CredentialsProvider(vertx: Vertx) : AwsCredentialsProvider {
    private val client = WebClient.create(vertx)
    override suspend fun resolve(): AwsCredentials {
        val token = client.put(80, "169.254.169.254", "/latest/api/token")
            .putHeader("X-aws-ec2-metadata-token-ttl-seconds", "21600")
            .send().coAwait().bodyAsString()
        val role = client.get(80, "169.254.169.254", "/latest/meta-data/iam/security-credentials/")
            .putHeader("X-aws-ec2-metadata-token", token).send().coAwait().bodyAsString()
        val creds = client.get(80, "169.254.169.254", "/latest/meta-data/iam/security-credentials/$role")
            .putHeader("X-aws-ec2-metadata-token", token).send().coAwait().bodyAsJsonObject()
        return AwsCredentials(
            accessKeyId = creds.getString("AccessKeyId"),
            secretKey   = creds.getString("SecretAccessKey"),
            sessionToken = creds.getString("Token"),
        )
    }
}
```

In production wrap `Imdsv2CredentialsProvider` with a refreshing cache —
credentials usually expire in 6 hours.

## 16.5  HTTP transport — `WebClient` reused

```kotlin
internal class HttpTransport(vertx: Vertx, private val opts: DynamoDbOptions) {
    private val client: WebClient = WebClient.create(vertx, WebClientOptions()
        .setMaxPoolSize(opts.maxConnections)
        .setKeepAlive(true)
        .setKeepAliveTimeout(opts.keepAliveSeconds)
        .setConnectTimeout(opts.connectTimeoutMs.toInt())
        .setIdleTimeout(opts.keepAliveSeconds)
        .setSsl(opts.endpoint?.startsWith("https") ?: true)
        .setTrustAll(false)
        .setVerifyHost(true)
        .setUserAgent("vertx-ddb-driver/0.1.0"))

    fun post(signed: SignedRequest): Future<HttpResponse<Buffer>> {
        val url = opts.resolvedEndpoint()
        return client.postAbs(url)
            .timeout(opts.requestTimeoutMs)
            .putHeaders(signed.headers)
            .sendBuffer(signed.body)
    }
}

data class SignedRequest(val headers: MultiMap, val body: Buffer)
```

`WebClient` underneath is just a `HttpClient` with conveniences. The pool
multiplexes requests over a small number of TCP connections to the DynamoDB
endpoint. **Key invariant: one `WebClient` instance per `Vertx`**; never
recreate per request.

## 16.6  SigV4 signing

SigV4 is the AWS request-signing algorithm. The TL;DR:

```
StringToSign = "AWS4-HMAC-SHA256\n"
             + ISO8601_TIMESTAMP + "\n"
             + DATE + "/" + REGION + "/" + SERVICE + "/aws4_request\n"
             + SHA256(CanonicalRequest)

CanonicalRequest = METHOD + "\n"
                 + CANONICAL_URI + "\n"
                 + CANONICAL_QUERY_STRING + "\n"
                 + CANONICAL_HEADERS + "\n"
                 + SIGNED_HEADERS + "\n"
                 + SHA256(BODY)

SigningKey = HMAC-SHA256(HMAC-SHA256(HMAC-SHA256(HMAC-SHA256(
    "AWS4" + secret, DATE), REGION), SERVICE), "aws4_request")

Signature = HMAC-SHA256(SigningKey, StringToSign)

Authorization: AWS4-HMAC-SHA256 Credential=<key>/<DATE>/<REGION>/<SERVICE>/aws4_request,
               SignedHeaders=<signed_headers>, Signature=<signature>
```

```kotlin
internal class Signer(
    private val credentialsProvider: AwsCredentialsProvider,
    private val region: String,
    private val service: String,
) {
    suspend fun sign(target: String, body: Buffer): SignedRequest {
        val creds = credentialsProvider.resolve()
        val now = Instant.now()
        val amzDate = AMZ_FMT.format(now)                  // "20260527T103014Z"
        val dateStamp = DATE_FMT.format(now)               // "20260527"

        val bodySha = sha256Hex(body.bytes)

        val headers = MultiMap.caseInsensitiveMultiMap()
        headers.add("host", "dynamodb.$region.amazonaws.com")
        headers.add("content-type", "application/x-amz-json-1.0")
        headers.add("x-amz-target", target)
        headers.add("x-amz-date", amzDate)
        headers.add("x-amz-content-sha256", bodySha)
        creds.sessionToken?.let { headers.add("x-amz-security-token", it) }

        val signedHeaders = headers.names().toSortedSet().joinToString(";") { it.lowercase() }
        val canonicalHeaders = headers.names().toSortedSet()
            .joinToString("\n") { "${it.lowercase()}:${headers.get(it).trim()}" } + "\n"

        val canonicalRequest = listOf(
            "POST",
            "/",
            "",
            canonicalHeaders,
            signedHeaders,
            bodySha,
        ).joinToString("\n")

        val credentialScope = "$dateStamp/$region/$service/aws4_request"
        val stringToSign = listOf(
            "AWS4-HMAC-SHA256",
            amzDate,
            credentialScope,
            sha256Hex(canonicalRequest.toByteArray()),
        ).joinToString("\n")

        val kDate    = hmac("AWS4${creds.secretKey}".toByteArray(), dateStamp.toByteArray())
        val kRegion  = hmac(kDate, region.toByteArray())
        val kService = hmac(kRegion, service.toByteArray())
        val kSigning = hmac(kService, "aws4_request".toByteArray())
        val signature = hex(hmac(kSigning, stringToSign.toByteArray()))

        headers.add(
            "Authorization",
            "AWS4-HMAC-SHA256 Credential=${creds.accessKeyId}/$credentialScope, " +
            "SignedHeaders=$signedHeaders, Signature=$signature"
        )
        return SignedRequest(headers, body)
    }

    companion object {
        private val AMZ_FMT  = DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'").withZone(ZoneOffset.UTC)
        private val DATE_FMT = DateTimeFormatter.ofPattern("yyyyMMdd").withZone(ZoneOffset.UTC)

        private fun sha256Hex(b: ByteArray): String {
            val md = MessageDigest.getInstance("SHA-256")
            return hex(md.digest(b))
        }
        private fun hmac(key: ByteArray, data: ByteArray): ByteArray {
            val mac = Mac.getInstance("HmacSHA256")
            mac.init(SecretKeySpec(key, "HmacSHA256"))
            return mac.doFinal(data)
        }
        private fun hex(b: ByteArray): String = b.joinToString("") { "%02x".format(it) }
    }
}
```

A few subtle things to get right:

- **Trim header values and lowercase header names** in the canonical headers.
- **Sort headers alphabetically.**
- **Hash the *raw body bytes*, not a JSON-pretty version.**
- **Include `x-amz-content-sha256` even though SigV4 doesn't strictly require
  it.** DynamoDB does.
- **`SecretKeySpec(key, "HmacSHA256")`** — the algorithm name matters.

### Why SigV4 stings on first attempt

The classic bugs:
- Spaces around `:` in canonical headers → `InvalidSignature`.
- Wrong date format (`Z` vs explicit `+0000`).
- Forgetting to include `x-amz-security-token` (for STS credentials).
- Missing trailing newline in `canonicalHeaders`.

Compare to `aws-cli --debug` output of a working request to debug; the
"canonical request" and "string to sign" are printed in the debug logs.

## 16.7  JSON shapes — `AttributeValue` and friends

DynamoDB's JSON is typed:

```json
{ "TableName": "users",
  "Key": { "id": { "S": "u-42" } } }
```

Wrap in Kotlin:

```kotlin
sealed class AttributeValue {
    data class S(val value: String) : AttributeValue()
    data class N(val value: String) : AttributeValue()      // numbers as strings in DDB
    data class B(val value: ByteArray) : AttributeValue()
    data class Bool(val value: Boolean) : AttributeValue()
    object Null : AttributeValue()
    data class L(val value: List<AttributeValue>) : AttributeValue()
    data class M(val value: Map<String, AttributeValue>) : AttributeValue()
    data class Ss(val value: Set<String>) : AttributeValue()
    data class Ns(val value: Set<String>) : AttributeValue()
}

data class GetItemRequest(
    val tableName: String,
    val key: Map<String, AttributeValue>,
    val consistentRead: Boolean = false,
    val projectionExpression: String? = null,
)

data class GetItemResponse(
    val item: Map<String, AttributeValue>?,
    val consumedCapacity: ConsumedCapacity? = null,
)
```

A small custom Jackson module renders the discriminated union as DDB's
JSON format:

```kotlin
class AttributeValueSerializer : JsonSerializer<AttributeValue>() {
    override fun serialize(value: AttributeValue, gen: JsonGenerator, p: SerializerProvider) {
        gen.writeStartObject()
        when (value) {
            is AttributeValue.S    -> gen.writeStringField("S", value.value)
            is AttributeValue.N    -> gen.writeStringField("N", value.value)
            is AttributeValue.Bool -> gen.writeBooleanField("BOOL", value.value)
            AttributeValue.Null    -> gen.writeBooleanField("NULL", true)
            is AttributeValue.L    -> { gen.writeFieldName("L"); gen.writeStartArray()
                                        value.value.forEach { serialize(it, gen, p) }; gen.writeEndArray() }
            is AttributeValue.M    -> { gen.writeFieldName("M"); gen.writeStartObject()
                                        value.value.forEach { (k, v) -> gen.writeFieldName(k); serialize(v, gen, p) }
                                        gen.writeEndObject() }
            else -> error("not yet implemented")
        }
        gen.writeEndObject()
    }
}
```

Symmetric deserializer reads the discriminator key and builds the right case.

## 16.8  Retries

DynamoDB returns 400 with error codes for transient conditions
(`ProvisionedThroughputExceededException`, `ThrottlingException`). The
canonical strategy is **exponential backoff with full jitter**:

```kotlin
internal class Retry(
    private val maxAttempts: Int = 4,
    private val baseDelayMs: Long = 50,
    private val maxDelayMs: Long = 5_000,
) {
    suspend fun <T> execute(block: suspend () -> T): T {
        var attempt = 0
        while (true) {
            try {
                return block()
            } catch (e: DynamoDbException) {
                attempt++
                if (attempt >= maxAttempts || !e.retryable) throw e
                val sleep = jitter(min(baseDelayMs * (1L shl attempt), maxDelayMs))
                delay(sleep)
            }
        }
    }
    private fun jitter(maxMs: Long) = ThreadLocalRandom.current().nextLong(maxMs + 1)
}
```

`delay(sleep)` is a *coroutine* delay — it doesn't block the event loop.

## 16.9  Error mapping

```kotlin
open class DynamoDbException(msg: String, val statusCode: Int = -1, val retryable: Boolean = false)
    : RuntimeException(msg)

class ConditionalCheckFailedException(msg: String) : DynamoDbException(msg, 400, false)
class ProvisionedThroughputExceededException(msg: String) : DynamoDbException(msg, 400, true)
class ResourceNotFoundException(msg: String) : DynamoDbException(msg, 400, false)
class ThrottlingException(msg: String) : DynamoDbException(msg, 400, true)
class InternalServerError(msg: String) : DynamoDbException(msg, 500, true)

private fun decodeClientError(resp: HttpResponse<Buffer>): DynamoDbException {
    val body = resp.bodyAsJsonObject() ?: return DynamoDbException("unknown", resp.statusCode())
    val type = body.getString("__type", "Unknown").substringAfterLast("#")
    val msg = body.getString("message", "")
    return when (type) {
        "ConditionalCheckFailedException"       -> ConditionalCheckFailedException(msg)
        "ProvisionedThroughputExceededException" -> ProvisionedThroughputExceededException(msg)
        "ResourceNotFoundException"             -> ResourceNotFoundException(msg)
        "ThrottlingException"                   -> ThrottlingException(msg)
        else -> DynamoDbException("$type: $msg", resp.statusCode())
    }
}
```

This is the *boundary translation* pattern (chapter 8.12 for PG, chapter 10
for gRPC). Translate wire-level errors into named domain exceptions at the
driver boundary.

## 16.10  Using the driver

```kotlin
class AppVerticle : CoroutineVerticle() {
    private lateinit var ddb: DynamoDbClient

    override suspend fun start() {
        ddb = DynamoDbClient.create(vertx, DynamoDbOptions(
            region = "us-east-1",
            credentials = EnvCredentialsProvider(),
            maxConnections = 32,
        ))

        val user = ddb.getItem(GetItemRequest(
            tableName = "users",
            key = mapOf("id" to AttributeValue.S("u-42")),
        )).item

        log.info("user={}", user)
    }
}
```

It composes seamlessly with the existing app — same `Vertx`, same event
loops, same Jackson, same metrics surface (you can register Micrometer
timers around `invoke()` for free).

## 16.11  Streaming — `Query` with paging

```kotlin
fun queryAllPages(req: QueryRequest): Flow<Map<String, AttributeValue>> = flow {
    var lastKey: Map<String, AttributeValue>? = null
    do {
        val pageReq = req.copy(exclusiveStartKey = lastKey)
        val page = ddb.query(pageReq)
        page.items?.forEach { emit(it) }
        lastKey = page.lastEvaluatedKey
    } while (lastKey != null)
}

// Caller:
queryAllPages(QueryRequest(...))
    .collect { item -> process(item) }
```

Kotlin `Flow` is a natural fit for paginated APIs and back-pressures cleanly.

## 16.12  Transactions

```kotlin
data class TransactWriteItemsRequest(val items: List<TransactWriteItem>)

sealed class TransactWriteItem {
    data class Put(val tableName: String, val item: Map<String, AttributeValue>) : TransactWriteItem()
    data class Update(val tableName: String, val key: Map<String, AttributeValue>,
                      val updateExpression: String, val attributeValues: Map<String, AttributeValue>) : TransactWriteItem()
    data class Delete(val tableName: String, val key: Map<String, AttributeValue>) : TransactWriteItem()
    data class ConditionCheck(val tableName: String, val key: Map<String, AttributeValue>,
                              val conditionExpression: String) : TransactWriteItem()
}

suspend fun transactWriteItems(req: TransactWriteItemsRequest): Unit =
    invoke("DynamoDB_20120810.TransactWriteItems", req, Unit::class)
```

`TransactWriteItems` is interesting because it can fail with a
`TransactionCanceledException` containing per-item reasons. Parse those out
and surface them as a structured Kotlin failure.

## 16.13  Metrics

```kotlin
private val timer = Timer.builder("ddb_operation_duration_seconds")
    .description("DynamoDB call latency")
    .tag("service", "dynamodb")
    .publishPercentiles(0.5, 0.95, 0.99)
    .register(BackendRegistries.getDefaultNow())

private suspend fun <Req: Any, Res: Any> invoke(target: String, req: Req, resType: KClass<Res>): Res {
    val sample = Timer.start()
    try {
        return ... // existing impl
    } finally {
        sample.stop(timer.tags("op", target.substringAfterLast(".")))
    }
}
```

Operationally: emit p50/p95/p99 per operation. Add a counter for retries —
high retry count indicates throttling or backend issues.

## 16.14  Testing

[localstack/localstack](https://github.com/localstack/localstack) ships a
DynamoDB emulator. Integration test:

```kotlin
@Testcontainers
class DynamoDbClientIT {
    @Container @JvmStatic
    val ddb = GenericContainer<Nothing>("localstack/localstack:3").apply {
        withEnv("SERVICES", "dynamodb")
        withExposedPorts(4566)
    }

    @Test
    fun `put then get`(vertx: Vertx) = vertxTest(vertx) {
        val client = DynamoDbClient.create(vertx, DynamoDbOptions(
            region = "us-east-1",
            endpoint = "http://${ddb.host}:${ddb.firstMappedPort}",
            credentials = StaticCredentialsProvider(AwsCredentials("test", "test")),
        ))

        // Create a table (using the AWS SDK or a one-off REST call)
        // ...

        client.putItem(PutItemRequest(tableName = "users", item = mapOf(
            "id" to AttributeValue.S("u-1"),
            "email" to AttributeValue.S("a@b.c"),
        )))

        val r = client.getItem(GetItemRequest(
            tableName = "users",
            key = mapOf("id" to AttributeValue.S("u-1")),
        ))

        assertThat(r.item?.get("email")).isEqualTo(AttributeValue.S("a@b.c"))
    }
}
```

Sign requests with `("test","test")` against LocalStack — it ignores SigV4
correctness but parses the headers like real DynamoDB.

## 16.15  What we didn't cover (and you might want)

- **Endpoint discovery** for VPC endpoints.
- **TLS pinning** for security-conscious workloads.
- **Connection-level metrics** (active connections, idle time).
- **Smart retries** — different error classes have different retry rules.
- **`DynamoDbStreams`** — a separate API for change-data-capture.
- **`TimeToLive`-aware item handling**.

These are all incremental additions on the same skeleton.

## 16.16  Why this matters

If you can build a DynamoDB driver, you can build any of:

- **S3** (multipart upload, SigV4, XML responses).
- **SQS** (long-polling, batch send).
- **Kinesis Data Streams** (HTTP/2, putRecords).
- **Custom internal services** at your company (proto over TCP).

The pattern is always:

1. Wrap an HTTP/TCP client with sane defaults (pool, keep-alive, timeouts).
2. Add the protocol-specific wire format (JSON/XML/Protobuf).
3. Add auth.
4. Wrap with typed suspending APIs.
5. Add retries with backoff.
6. Map errors to domain exceptions.
7. Add metrics.
8. Test against a local emulator.

## 16.17  Try it

1. Implement `BatchWriteItem`. Pay attention to "unprocessed items" — the
   API returns them and your client should re-submit until empty.
2. Add a Micrometer histogram for **payload sizes** (request and response).
3. Build a simple S3 driver using the same skeleton. The protocol is XML
   instead of JSON; SigV4 has slight variations (chunked uploads, multipart
   etag computation). Try uploading a 10 MB blob via `WebClient.sendBuffer`.

[← Ch 15](15-netty.md) · [Next: Chapter 17 — Production best practices →](17-production.md)
