# go-otel-2026

Go アプリに OpenTelemetry の traces / metrics / logs を計装し、OpenTelemetry Collector
経由で Grafana スタックに送る最小構成。ブログ記事「OpenTelemetry を Go で学び直す」の
サンプルコード。

```
                 OTLP/gRPC                 OTLP/gRPC
+-----------+   (4317)    +----------------+   (4317)   +------------------------+
| order-api | ----------> | OTel Collector | ---------> | grafana/otel-lgtm      |
|  (Go)     |             |  memory_limiter|            |  Tempo  (traces)       |
|           |             |  resource      |            |  Prometheus (metrics)  |
|           |             |  transform     |            |  Loki   (logs)         |
|           |             |  batch         |            |  Grafana (UI :3000)    |
+-----------+             +----------------+            +------------------------+
```

## 構成ファイル

| ファイル | 内容 |
|---|---|
| `app/otel.go` | SDK 初期化。resource / propagator / TracerProvider / MeterProvider / LoggerProvider と shutdown |
| `app/main.go` | HTTP サーバ。otelhttp、手動スパン、カスタムメトリクス、slog ブリッジ |
| `otel-collector-config.yaml` | receiver / processor / exporter のパイプライン定義 |
| `docker-compose.yml` | アプリ + Collector + Grafana スタック |

## 使用バージョン

2026-09 時点。

| コンポーネント | バージョン |
|---|---|
| `go.opentelemetry.io/otel`（traces / metrics） | v1.46.0（stable） |
| `go.opentelemetry.io/otel/log`, `sdk/log` | v0.22.0（beta） |
| `go.opentelemetry.io/contrib/...` | v0.71.0 / bridges/otelslog v0.20.1 |
| semconv | v1.43.0 |
| OpenTelemetry Collector contrib | 0.161.0 |
| grafana/otel-lgtm | 0.33.0 |

Logs API / SDK は 2026-08-27 に v1.47.0-rc.1 が出ているが、プレリリースのため
`go get @latest` では解決されない。ここでは beta の v0.22.0 を使っている。

## 起動

```bash
docker compose up -d --build
```

## 動作確認

```bash
# 注文を作る（正常系）
curl http://localhost:8080/orders

# 計装済みクライアントから自分自身を呼ぶ（trace context の伝播確認）
curl http://localhost:8080/chain

# 失敗する経路（span.RecordError / SetStatus の確認）
curl http://localhost:8080/orders/error

# 計装していないエンドポイント
curl http://localhost:8080/healthz
```

Collector が受け取った内容は debug exporter がログに出す。

```bash
docker compose logs otel-collector | grep chargePayment
```

Grafana は http://localhost:3000 （認証なし）。Explore から Tempo / Prometheus / Loki を参照する。

- traces: Tempo で `{ resource.service.name = "order-api" }`
- metrics: Prometheus で `app_orders_created_total`
- logs: Loki で `{service_name="order-api"}`

ログには `trace_id` と `span_id` が入っているため、Loki のログから Tempo のトレースへ辿れる。

## サンプリング比率を変える

`docker-compose.yml` の `OTEL_TRACES_SAMPLER_ARG` を変えて再起動する。

```yaml
OTEL_TRACES_SAMPLER_ARG: "0.1"  # 10% だけサンプル
```

## 停止

```bash
docker compose down
```
