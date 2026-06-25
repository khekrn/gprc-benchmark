package com.beam.bench;

import org.springframework.data.repository.CrudRepository;

/**
 * Spring Data JDBC repository for {@link OutboxEvent}. {@code save()} on a
 * null-id entity issues the outbox INSERT inside the ExecuteTx transaction.
 */
interface OutboxRepository extends CrudRepository<OutboxEvent, Long> {
}
