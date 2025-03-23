package telemetry

import (
	"context"
	"go.opentelemetry.io/otel"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/detectors/aws/ecs"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/IliaW/rule-api/config"
	"github.com/google/uuid"
)

var meter metric.Meter

type MetricsProvider struct {
	ApiMetrics *ApiMetrics
	Close      func()
}

type ApiMetrics struct {
	SuccessResponseCounter func(count int64)
	ErrorResponseCounter   func(count int64)
}

func SetupMetrics(ctx context.Context, cfg *config.Config) *MetricsProvider {
	metricsProvider := new(MetricsProvider)
	var meterProvider *sdkmetric.MeterProvider

	if cfg.TelemetrySettings.Enabled {
		r, err := newResource(cfg)
		if err != nil {
			slog.Error("failed to get resource.", slog.String("err", err.Error()))
			os.Exit(1)
		}
		exporter, err := newMetricExporter(ctx, cfg.TelemetrySettings)
		if err != nil {
			slog.Error("failed to get metric exporter.", slog.String("err", err.Error()))
			os.Exit(1)
		}
		meterProvider = newMeterProvider(exporter, *r)
		otel.SetMeterProvider(meterProvider)
	}

	meter = otel.Meter(cfg.ServiceName)
	metricsProvider.Close = func() {
		if meterProvider != nil {
			err := meterProvider.Shutdown(ctx)
			if err != nil {
				slog.Error("failed to shutdown metrics provider.", slog.String("err", err.Error()))
			}
		}
	}

	successResponseCounter, err := meter.Int64Counter("rule-api.response.success",
		metric.WithDescription("The number of success responses from [get] /crawl-allowed."),
		metric.WithUnit("{messages}"))
	errorResponseCounter, err := meter.Int64Counter("rule-api.response.error",
		metric.WithDescription("The number of error responses from [get] /crawl-allowed."),
		metric.WithUnit("{messages}"))
	if err != nil {
		slog.Error("failed to create telemetry counters for the rule api.", slog.String("err", err.Error()))
		os.Exit(1)
	}
	metricsProvider.ApiMetrics = &ApiMetrics{
		SuccessResponseCounter: func(count int64) {
			if cfg.TelemetrySettings.Enabled {
				successResponseCounter.Add(ctx, count)
			}
		},
		ErrorResponseCounter: func(count int64) {
			if cfg.TelemetrySettings.Enabled {
				errorResponseCounter.Add(ctx, count)
			}
		},
	}

	// initialize metrics in DataDog for setup UI
	if cfg.TelemetrySettings.Enabled {
		metricsProvider.ApiMetrics.SuccessResponseCounter(1)
		metricsProvider.ApiMetrics.ErrorResponseCounter(1)
	}

	return metricsProvider
}

func newResource(cfg *config.Config) (*resource.Resource, error) {
	ecsResourceDetector := ecs.NewResourceDetector()
	ecsResource, err := ecsResourceDetector.Detect(context.Background())
	if err != nil {
		slog.Error("ecs detection failed", slog.String("err", err.Error()))
	}
	mergedResource, err := resource.Merge(ecsResource, resource.Default())
	if err != nil {
		slog.Error("failed to merge resources", slog.String("err", err.Error()))
	}
	keyValue, found := ecsResource.Set().Value("container.id")
	var serviceId string
	if found {
		serviceId = keyValue.AsString()
	} else {
		serviceId = uuid.New().String()
	}
	return resource.Merge(mergedResource,
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.DeploymentEnvironment(cfg.Env),
			semconv.ServiceInstanceID(serviceId),
		))
}

func newMetricExporter(ctx context.Context, cfg *config.TelemetryConfig) (sdkmetric.Exporter, error) {
	return otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(cfg.CollectorUrl),
		otlpmetrichttp.WithInsecure())
}

func newMeterProvider(meterExporter sdkmetric.Exporter, resource resource.Resource) *sdkmetric.MeterProvider {
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(meterExporter)),
		sdkmetric.WithResource(&resource),
	)
	return meterProvider
}
