package com.beam.bench

import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication

/**
 * Boot entry point. Spring Boot's Spring Data R2DBC auto-configuration scans this
 * package for `@Table` entities and `CoroutineCrudRepository` interfaces and
 * wires them to the R2DBC `ConnectionFactory`, and auto-configures an
 * `R2dbcTransactionManager` that backs the `@Transactional` suspend function in
 * [Db.executeTx]. No explicit beans needed.
 */
@SpringBootApplication
class SpringRtApplication

fun main(args: Array<String>) {
    runApplication<SpringRtApplication>(*args)
}
