package com.beam.bench;

import org.springframework.data.repository.CrudRepository;

/**
 * Spring Data JDBC repository for {@link Command}. {@code save()} on an entity
 * with a null {@code @Id} issues the autocommit INSERT and returns the row with
 * its generated id — the repository-layer equivalent of spring-vt's
 * {@code INSERT ... RETURNING id}.
 */
interface CommandRepository extends CrudRepository<Command, Long> {
}
