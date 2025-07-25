package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer trace.Tracer
)

func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	// OTLPエクスポーターの設定
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	// gRPCクライアントの作成
	conn, err := grpc.DialContext(ctx, otlpEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// トレースエクスポーターの作成
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// リソースの作成
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("go-sample-app"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// TracerProviderの作成
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	return tp, nil
}

func initMeter(ctx context.Context) (*metric.MeterProvider, error) {
	// OTLPエクスポーターの設定
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	// gRPCクライアントの作成
	conn, err := grpc.DialContext(ctx, otlpEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// メトリクスエクスポーターの作成
	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	// リソースの作成
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("go-sample-app"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// MeterProviderの作成
	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)

	otel.SetMeterProvider(mp)
	return mp, nil
}

func handleHello(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// トレースの開始
	ctx, span := tracer.Start(ctx, "handleHello")
	defer span.End()

	// ランダムな遅延を追加（トレースで確認するため）
	time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)

	// メトリクスの記録
	if m := otel.GetMeterProvider().Meter("go-sample-app"); m != nil {
		requestCounter, _ := m.Int64Counter("http_requests_total")
		requestCounter.Add(ctx, 1)
	}

	// レスポンスの生成
	response := fmt.Sprintf("Hello, OpenTelemetry! Time: %s", time.Now().Format(time.RFC3339))
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(response))
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// トレースの開始
	ctx, span := tracer.Start(ctx, "handleMetrics")
	defer span.End()

	// カスタムメトリクスの記録
	if m := otel.GetMeterProvider().Meter("go-sample-app"); m != nil {
		customCounter, _ := m.Int64Counter("custom_operations_total")
		customCounter.Add(ctx, 1)

		// ヒストグラムの記録
		histogram, _ := m.Float64Histogram("request_duration_seconds")
		histogram.Record(ctx, float64(time.Now().UnixNano())/1e9)
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Metrics recorded"))
}

func main() {
	ctx := context.Background()

	// トレーサーの初期化
	tp, err := initTracer(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	// メーターの初期化
	mp, err := initMeter(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := mp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down meter provider: %v", err)
		}
	}()

	// グローバルトレーサーの取得
	tracer = otel.Tracer("go-sample-app")

	// HTTPサーバーの設定
	http.HandleFunc("/", handleHello)
	http.HandleFunc("/metrics", handleMetrics)

	port := "8080"
	log.Printf("Starting server on port %s", port)
	log.Printf("Available endpoints:")
	log.Printf("  - http://localhost:%s/ (Hello endpoint)", port)
	log.Printf("  - http://localhost:%s/metrics (Metrics endpoint)", port)
	log.Printf("  - http://localhost:9090 (Prometheus)")
	log.Printf("  - http://localhost:16686 (Jaeger)")
	log.Printf("  - http://localhost:3000 (Grafana)")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
