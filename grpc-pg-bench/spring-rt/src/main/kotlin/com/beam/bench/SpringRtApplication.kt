package com.beam.bench

import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication
import org.springframework.context.annotation.Bean
import org.springframework.transaction.ReactiveTransactionManager
import org.springframework.transaction.reactive.TransactionalOperator

@SpringBootApplication
class SpringRtApplication {
    /**
     * Boot auto-configures an R2dbcTransactionManager (a ReactiveTransactionManager)
     * from the ConnectionFactory; wrap it in a TransactionalOperator for the
     * coroutine `executeAndAwait` transaction in [Db.executeTx].
     */
    @Bean
    fun transactionalOperator(rtm: ReactiveTransactionManager): TransactionalOperator =
        TransactionalOperator.create(rtm)
}

fun main(args: Array<String>) {
    runApplication<SpringRtApplication>(*args)
}
