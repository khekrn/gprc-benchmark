package com.example.app.observability

import io.micrometer.core.instrument.MeterRegistry
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import io.vertx.micrometer.MicrometerMetricsFactory
import io.vertx.micrometer.MicrometerMetricsOptions
import io.vertx.micrometer.VertxPrometheusOptions
import io.vertx.micrometer.backends.BackendRegistries

object Metrics {

    @JvmStatic
    val registry: PrometheusMeterRegistry =
        PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    fun options(): MicrometerMetricsOptions =
        MicrometerMetricsOptions()
            .setPrometheusOptions(VertxPrometheusOptions().setEnabled(true))
            .setEnabled(true)
            .setJvmMetricsEnabled(true)

    /**
     * Vert.x 5 no longer accepts a pre-built registry on the options object.
     * Instead you supply a [MicrometerMetricsFactory] to the Vertx builder:
     *   Vertx.builder().with(opts).withMetrics(Metrics.factory()).build()
     */
    fun factory(): MicrometerMetricsFactory = MicrometerMetricsFactory(registry)

    fun meterRegistry(): MeterRegistry = BackendRegistries.getDefaultNow() ?: registry
}
