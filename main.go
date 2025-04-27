package main

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkResource "go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	otelTrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	requestCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "A counter for requests to the wrapped handler.",
		},
	)
)

func main() {
	ctx := context.Background()

	logger := SetupLogger()

	tp := MustLoadTraceProvider(logger)
	defer func() { _ = tp.Shutdown(ctx) }()

	prometheus.MustRegister(requestCounter)

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, span := StartSpanFromContext(r.Context(), "page /")
		defer span.End()

		requestCounter.Inc()

		span.AddEvent("initial logger")
		log := logger.With(
			zap.Field{Key: "method", Type: zapcore.StringType, String: r.Method},
			zap.Field{Key: "url", Type: zapcore.StringType, String: r.URL.String()},
			zap.Field{Key: "userAgent", Type: zapcore.StringType, String: r.UserAgent()},
		)

		span.AddEvent("logging request")
		log.Info("Received request",
			zap.Time("@timestamp", time.Now()),
		)

		span.AddEvent("sending response")
		w.Write([]byte("Hello World"))
	})
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, span := StartSpanFromContext(r.Context(), "page /hello")
		defer span.End()

		requestCounter.Inc()

		span.AddEvent("initial logger")
		log := logger.With(
			zap.Field{Key: "method", Type: zapcore.StringType, String: r.Method},
			zap.Field{Key: "url", Type: zapcore.StringType, String: r.URL.String()},
			zap.Field{Key: "userAgent", Type: zapcore.StringType, String: r.UserAgent()},
		)

		name := r.URL.Query().Get("name")
		span.AddEvent("check name")
		if name == "" {
			span.AddEvent("logging error")
			log.Error("name is empty",
				zap.Time("@timestamp", time.Now()),
			)

			span.AddEvent("sending error response")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("name is empty"))
			return
		}

		span.AddEvent("logging request")
		log.Info("Received request",
			zap.String("name", name),
			zap.Time("@timestamp", time.Now()),
		)

		span.AddEvent("sending response")
		w.Write([]byte("Hello, " + name))
	})

	httpAddr := ":8080"
	logger.Info("Listening server on " + httpAddr)
	err := http.ListenAndServe(httpAddr, mux)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func MustLoadTraceProvider(logger *zap.Logger) *trace.TracerProvider {
	url := fmt.Sprintf("%s:%d", "jaeger", 4318)
	explorer, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint(url),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		logger.Fatal("Failed to create trace provider", zap.Error(err))
		panic(err.Error())
	}

	otel.SetTextMapPropagator(propagation.TraceContext{})

	traceProvider := trace.NewTracerProvider(
		trace.WithBatcher(explorer),
		trace.WithResource(sdkResource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("test-tech"),
			semconv.DeploymentEnvironment("local"),
		)),
	)

	otel.SetTracerProvider(traceProvider)

	return traceProvider
}

func StartSpanFromContext(ctx context.Context, name string, opts ...otelTrace.SpanStartOption) (context.Context, otelTrace.Span) {
	return otel.GetTracerProvider().Tracer("test-tech").Start(ctx, name, opts...)
}

func SetupLogger() *zap.Logger {
	filename := "./logs/app.log"
	logFile, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	writeSyncer := zapcore.AddSync(logFile)

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "@timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		writeSyncer,
		zapcore.InfoLevel,
	)

	logger := zap.New(core)
	defer logger.Sync()
	logger = logger.With(zap.Field{
		Key:    "service",
		Type:   zapcore.StringType,
		String: "test-tech",
	})

	return logger
}
