package com.beam.bench;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/**
 * Boot entry point. Spring Boot's Spring Data JDBC auto-configuration scans this
 * package for {@code @Table} entities and {@code Repository} interfaces and
 * wires them to the HikariCP {@code DataSource}, so no explicit
 * {@code @EnableJdbcRepositories} is needed.
 */
@SpringBootApplication
public class SpringDataJdbcApplication {
    public static void main(String[] args) {
        SpringApplication.run(SpringDataJdbcApplication.class, args);
    }
}
