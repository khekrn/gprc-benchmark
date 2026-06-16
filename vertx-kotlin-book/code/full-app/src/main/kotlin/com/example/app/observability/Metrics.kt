package com.example.app.observability

import io.micrometer.core.instrument.MeterRegistry
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import io.vertx.micrometer.MicrometerMetricsOptions
import io.vertx.micrometer.VertxPrometheusOptions
import io.vertx.micrometer.backends.BackendRegistries

object Metrics {

    @JvmStatic
    val registry: PrometheusMeterRegistry =
        PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    fun options(): MicrometerMetricsOptions =
        MicrometerMetricsOptions()
            .setMicrometerRegistry(registry)
            .setPrometheusOptions(VertxPrometheusOptions().setEnabled(true))
            .setEnabled(true)
            .setJvmMetricsEnabled(true)

    fun meterRegistry(): MeterRegistry = BackendRegistries.getDefaultNow() ?: registry
}
