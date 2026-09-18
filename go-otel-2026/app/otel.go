package main

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const (
	serviceName    = "order-api"
	serviceVersion = "0.1.0"
)

// setupOTelSDK は traces / metrics / logs の 3 シグナルを初期化し、
// すべての provider をまとめて止める shutdown 関数を返す。
//
// エクスポート先は OTEL_EXPORTER_OTLP_ENDPOINT 環境変数で指定する。
// 例: OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
// スキームが http の場合、gRPC エクスポーターは TLS なしで接続する。
func setupOTelSDK(ctx context.Context) (func(context.Context) error, error) {
	var shutdownFuncs []func(context.Context) error

	// 初期化済みの provider を逆順に止める。
	// 途中で失敗しても、それまでに作った provider は必ず止める。
	shutdown := func(ctx context.Context) error {
		var err error
		for i := len(shutdownFuncs) - 1; i >= 0; i-- {
			err = errors.Join(err, shutdownFuncs[i](ctx))
		}
		shutdownFuncs = nil
		return err
	}

	res, err := newResource(ctx)
	if err != nil {
		return nil, err
	}

	// W3C Trace Context と Baggage を伝播させる。
	// 設定を忘れるとサービスをまたいだ時点でトレースが分断される。
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tp, err := newTracerProvider(ctx, res)
	if err != nil {
		return nil, errors.Join(err, shutdown(ctx))
	}
	shutdownFuncs = append(shutdownFuncs, tp.Shutdown)
	otel.SetTracerProvider(tp)

	mp, err := newMeterProvider(ctx, res)
	if err != nil {
		return nil, errors.Join(err, shutdown(ctx))
	}
	shutdownFuncs = append(shutdownFuncs, mp.Shutdown)
	otel.SetMeterProvider(mp)

	lp, err := newLoggerProvider(ctx, res)
	if err != nil {
		return nil, errors.Join(err, shutdown(ctx))
	}
	shutdownFuncs = append(shutdownFuncs, lp.Shutdown)
	global.SetLoggerProvider(lp)

	return shutdown, nil
}

// newResource は「このテレメトリを出しているのは誰か」を表す Resource を作る。
// service.name はバックエンドがサービスを識別する主キーになるため必ず設定する。
func newResource(ctx context.Context) (*resource.Resource, error) {
	env := os.Getenv("DEPLOYMENT_ENV")
	if env == "" {
		env = "local"
	}

	// resource.Default() には telemetry.sdk.* が含まれる。
	// Merge には両者の SchemaURL が一致している必要がある。
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironmentNameKey.String(env),
		),
	)
}

func newTracerProvider(ctx context.Context, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	// ParentBased でラップすることで、親がサンプルされたトレースの子スパンは
	// 必ずサンプルされる。ラップしないとトレースが虫食いになる。
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRatio()))

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	), nil
}

// samplingRatio は OTEL_TRACES_SAMPLER_ARG を 0.0〜1.0 の比率として読む。
// 未設定・不正値の場合は 1.0（全件サンプル）を返す。
func samplingRatio() float64 {
	v := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if v == "" {
		return 1.0
	}
	ratio, err := strconv.ParseFloat(v, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return 1.0
	}
	return ratio
}

func newMeterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	// PeriodicReader が一定間隔でメトリクスを収集して送る。
	// デフォルトは 60 秒だが、動作確認しやすいよう 10 秒にしている。
	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(10*time.Second))

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	), nil
}

func newLoggerProvider(ctx context.Context, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	), nil
}
