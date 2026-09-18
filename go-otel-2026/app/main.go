package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// 計装スコープ名。どのライブラリ・パッケージが出したテレメトリかを表す。
const scopeName = "github.com/jedipunkz/opentelemetry-playground/go-otel-2026/app"

var (
	tracer trace.Tracer
	logger *slog.Logger

	// 計測器はリクエストごとではなく起動時に 1 度だけ作る。
	ordersCreated metric.Int64Counter
	orderAmount   metric.Float64Histogram
)

// 計装済みの HTTP クライアント。
// Transport を差し替えるだけで、送信リクエストに traceparent ヘッダが付く。
var httpClient = &http.Client{
	Transport: otelhttp.NewTransport(http.DefaultTransport),
	Timeout:   5 * time.Second,
}

func main() {
	// SIGINT / SIGTERM でキャンセルされる context。
	// これがないと停止時に未送信のテレメトリが失われる。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	shutdown, err := setupOTelSDK(ctx)
	if err != nil {
		log.Fatalf("failed to set up OpenTelemetry: %v", err)
	}

	tracer = otel.Tracer(scopeName)
	logger = otelslog.NewLogger(scopeName)

	if err := initInstruments(); err != nil {
		log.Fatalf("failed to create instruments: %v", err)
	}

	// Go ランタイムのメトリクス（GC, goroutine 数, ヒープ）を自動収集する。
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		log.Fatalf("failed to start runtime instrumentation: %v", err)
	}

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      newRouter(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", srv.Addr)
		srvErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-srvErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	case <-ctx.Done():
		stop()
	}

	// サーバを止めてから SDK を止める。順番を逆にすると
	// 処理中のリクエストが出したスパンを送れない。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	if err := shutdown(shutdownCtx); err != nil {
		log.Printf("otel shutdown error: %v", err)
	}
	log.Print("shutdown complete")
}

func initInstruments() error {
	meter := otel.Meter(scopeName)

	var err error
	// 計測器の名前は semantic conventions に倣って . 区切りにする。
	ordersCreated, err = meter.Int64Counter(
		"app.orders.created",
		metric.WithDescription("作成された注文の件数"),
		metric.WithUnit("{order}"),
	)
	if err != nil {
		return err
	}

	orderAmount, err = meter.Float64Histogram(
		"app.order.amount",
		metric.WithDescription("注文金額の分布"),
		metric.WithUnit("JPY"),
	)
	return err
}

func newRouter() http.Handler {
	mux := http.NewServeMux()

	// パスごとに otelhttp でラップし、span 名にルートパターンを使う。
	// span 名に実際の URL（/orders/12345）を使うとカーディナリティが爆発する。
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, otelhttp.NewHandler(h, pattern))
	}

	handle("GET /orders", handleCreateOrder)
	handle("GET /orders/error", handleFailingOrder)
	handle("GET /chain", handleChain)

	// ヘルスチェックは計装しない。数が多く、トレースとして価値がない。
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok\n")
	})

	return mux
}

// handleCreateOrder は注文を作る。otelhttp が作ったサーバスパンの下に
// validateOrder と chargePayment の子スパンがぶら下がる。
func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID := fmt.Sprintf("ord-%05d", rand.IntN(100000))
	amount := float64(rand.IntN(90000)+1000) / 10

	// otelhttp が作った親スパンに属性を足す。
	// span の属性は高カーディナリティでもよい（メトリクスとは違う）。
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("order.id", orderID))

	if err := validateOrder(ctx, amount); err != nil {
		recordFailure(ctx, w, err, http.StatusBadRequest)
		return
	}
	if err := chargePayment(ctx, orderID, amount); err != nil {
		recordFailure(ctx, w, err, http.StatusBadGateway)
		return
	}

	ordersCreated.Add(ctx, 1, metric.WithAttributes(
		// メトリクスの属性は取りうる値が有限のものだけにする。
		attribute.String("order.status", "created"),
	))
	orderAmount.Record(ctx, amount)

	// InfoContext に ctx を渡すと、otelslog がログに trace_id と span_id を埋める。
	// Info（ctx なし）ではトレースと紐づかない。
	logger.InfoContext(ctx, "order created",
		slog.String("order.id", orderID),
		slog.Float64("order.amount", amount),
	)

	fmt.Fprintf(w, "order %s created: %.1f JPY\n", orderID, amount)
}

// handleFailingOrder は必ず失敗する経路。エラーの記録方法を示す。
func handleFailingOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := errors.New("payment gateway timeout")
	recordFailure(ctx, w, err, http.StatusBadGateway)
}

// handleChain は計装済みクライアントで自分自身の /orders を呼ぶ。
// クライアントスパンとサーバスパンが 1 本のトレースに繋がることを確認できる。
func handleChain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	upstream := os.Getenv("UPSTREAM_URL")
	if upstream == "" {
		upstream = "http://localhost:8080"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream+"/orders", nil)
	if err != nil {
		recordFailure(ctx, w, err, http.StatusInternalServerError)
		return
	}

	// ctx を渡すことで traceparent ヘッダが伝播する。
	resp, err := httpClient.Do(req)
	if err != nil {
		recordFailure(ctx, w, err, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		recordFailure(ctx, w, err, http.StatusBadGateway)
		return
	}

	fmt.Fprintf(w, "upstream responded: %s", body)
}

// validateOrder は入力検証を行う子スパンを作る。
func validateOrder(ctx context.Context, amount float64) error {
	_, span := tracer.Start(ctx, "validateOrder")
	defer span.End()

	span.SetAttributes(attribute.Float64("order.amount", amount))
	time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)

	if amount <= 0 {
		return errors.New("amount must be positive")
	}
	return nil
}

// chargePayment は外部の決済サービス呼び出しを模した子スパンを作る。
func chargePayment(ctx context.Context, orderID string, amount float64) error {
	ctx, span := tracer.Start(ctx, "chargePayment",
		// 外部サービス呼び出しなので Client として記録する。
		trace.WithSpanKind(trace.SpanKindClient),
	)
	defer span.End()

	span.SetAttributes(
		attribute.String("order.id", orderID),
		attribute.Float64("order.amount", amount),
	)

	time.Sleep(time.Duration(50+rand.IntN(150)) * time.Millisecond)

	// 10 回に 1 回失敗させ、エラーのトレースも観測できるようにする。
	if rand.IntN(10) == 0 {
		return errors.New("payment declined")
	}

	logger.DebugContext(ctx, "payment charged", slog.String("order.id", orderID))
	return nil
}

// recordFailure はエラーをスパン・ログ・メトリクスの 3 箇所に記録する。
func recordFailure(ctx context.Context, w http.ResponseWriter, err error, status int) {
	span := trace.SpanFromContext(ctx)
	// RecordError は例外イベントを追加するだけ。
	// SetStatus を呼ばないとスパンはエラー扱いにならない。
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())

	ordersCreated.Add(ctx, 1, metric.WithAttributes(
		attribute.String("order.status", "failed"),
	))

	logger.ErrorContext(ctx, "order failed", slog.String("error", err.Error()))
	http.Error(w, err.Error(), status)
}
