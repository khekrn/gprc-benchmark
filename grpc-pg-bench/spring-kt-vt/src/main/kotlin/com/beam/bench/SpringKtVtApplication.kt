package com.beam.bench

import org.jetbrains.exposed.v1.spring.boot4.autoconfigure.ExposedAutoConfiguration
import org.springframework.boot.autoconfigure.ImportAutoConfiguration
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication

/**
 * Wiring follows the official JetBrains `samples/exposed-spring` (Boot 4):
 * the `exposed-spring-boot4-starter` provides [ExposedAutoConfiguration], which
 * registers a `SpringTransactionManager` over Boot's HikariCP `DataSource`.
 * `@ImportAutoConfiguration` pulls that autoconfig in, after which any
 * `@Transactional` bean can call the Exposed DSL directly — Spring owns the
 * transaction boundary and hands Exposed the pooled connection.
 */
@SpringBootApplication
@ImportAutoConfiguration(ExposedAutoConfiguration::class)
class SpringKtVtApplication

fun main(args: Array<String>) {
    runApplication<SpringKtVtApplication>(*args)
}
