package com.example.app

import com.example.app.db.DbMigrator
import com.example.app.db.UserRepository
import com.example.app.domain.NewUser
import com.example.app.domain.UserError
import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.pgclient.PgBuilder
import io.vertx.pgclient.PgConnectOptions
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.PoolOptions
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import org.testcontainers.containers.PostgreSQLContainer

/**
 * Integration test against a real Postgres in Testcontainers.  We use
 * runTest so the suspend test body returns a CoroutineScope; we use the
 * unconfined dispatcher only because everything we await is async I/O.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class UserRepositoryIT {

    private val pg = PostgreSQLContainer("postgres:17-alpine")
        .withDatabaseName("appdb")
        .withUsername("app")
        .withPassword("app")

    private lateinit var vertx: Vertx
    private lateinit var pool: Pool
    private lateinit var repo: UserRepository

    @BeforeAll
    fun setUp() = runTest {
        pg.start()
        vertx = Vertx.vertx()
        val opts = PgConnectOptions()
            .setHost(pg.host).setPort(pg.firstMappedPort)
            .setDatabase("appdb").setUser("app").setPassword("app")
        pool = PgBuilder.pool().with(PoolOptions().setMaxSize(4))
            .connectingTo(opts).using(vertx).build()
        DbMigrator.migrate(pool)
        repo = UserRepository(pool)
    }

    @AfterAll
    fun tearDown() = runTest {
        pool.close().coAwait()
        vertx.close().coAwait()
        pg.stop()
    }

    @Test
    fun `create and read back`() = runTest {
        val created = repo.create(NewUser("a@x.io", "Alice"))
        val read = repo.findById(created.id)
        assertThat(read).isNotNull
        assertThat(read!!.email).isEqualTo("a@x.io")
    }

    @Test
    fun `duplicate email rejected`() = runTest {
        repo.create(NewUser("dup@x.io", "First"))
        assertThatThrownBy {
            kotlinx.coroutines.runBlocking { repo.create(NewUser("dup@x.io", "Second")) }
        }.isInstanceOf(UserError.DuplicateEmail::class.java)
    }

    @Test
    fun `streamAll yields rows`() = runTest {
        repeat(5) { i -> repo.create(NewUser("u$i@x.io", "User $i")) }
        val all = repo.streamAll(null).toList()
        assertThat(all).hasSizeGreaterThanOrEqualTo(5)
    }
}
