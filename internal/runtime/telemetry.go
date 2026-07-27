package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	foundationtelemetry "github.com/yueli-official/foundation/go/telemetry"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type TelemetryShutdown func(context.Context) error

func StartTelemetry(ctx context.Context, fallbackServiceName string) (TelemetryShutdown, error) {
	enabled, err := telemetryEnabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	sampler, err := telemetrySampler()
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	serviceName := firstNonEmpty(
		os.Getenv("OTEL_SERVICE_NAME"),
		os.Getenv("PLATFORM_SERVICE_NAME"),
		fallbackServiceName,
	)
	serviceVersion := firstNonEmpty(os.Getenv("OTEL_SERVICE_VERSION"), os.Getenv("PLATFORM_SERVICE_VERSION"))
	environment := firstNonEmpty(os.Getenv("PLATFORM_ENVIRONMENT"), "local")
	provider, err := foundationtelemetry.NewProvider(ctx, foundationtelemetry.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Environment:    environment,
		Attributes:     map[string]string{"platform.catalog.environment": environment},
		Exporter:       exporter,
		Sampler:        sampler,
	})
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	if err := foundationtelemetry.InstallGlobal(provider); err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	return provider.Shutdown, nil
}

func ShutdownTelemetry(shutdown TelemetryShutdown) {
	_ = foundationtelemetry.ShutdownWithTimeout(shutdown, 5*time.Second)
}

func TelemetryHTTPClient(client *http.Client) *http.Client {
	return foundationtelemetry.HTTPClient(client)
}

func telemetryEnabled() (bool, error) {
	if disabled := strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")); disabled != "" {
		value, err := strconv.ParseBool(disabled)
		if err != nil {
			return false, fmt.Errorf("OTEL_SDK_DISABLED must be a boolean: %w", err)
		}
		if value {
			return false, nil
		}
	}
	exporter := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")))
	if exporter == "none" {
		return false, nil
	}
	if exporter != "" && exporter != "otlp" {
		return false, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER %q; use otlp or none", exporter)
	}
	protocol := firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"), os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	if protocol != "" && !strings.EqualFold(protocol, "http/protobuf") {
		return false, fmt.Errorf("unsupported OTLP trace protocol %q; use http/protobuf", protocol)
	}
	endpointConfigured := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
	return exporter == "otlp" || endpointConfigured, nil
}

func telemetrySampler() (sdktrace.Sampler, error) {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	switch name {
	case "", "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "traceidratio", "parentbased_traceidratio":
		ratio := 1.0
		if raw := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); raw != "" {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				return nil, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
			}
			ratio = parsed
		}
		ratioSampler := sdktrace.TraceIDRatioBased(ratio)
		if name == "parentbased_traceidratio" {
			return sdktrace.ParentBased(ratioSampler), nil
		}
		return ratioSampler, nil
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_SAMPLER %q", name)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
