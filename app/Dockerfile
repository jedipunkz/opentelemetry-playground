# ビルドステージ
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 依存関係のコピー
COPY go.mod go.sum ./

# 依存関係のダウンロード
RUN go mod download

# ソースコードのコピー
COPY . .

# バイナリのビルド
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# 実行ステージ
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# ビルドステージからバイナリをコピー
COPY --from=builder /app/main .

# ポートの公開
EXPOSE 8080

# アプリケーションの実行
CMD ["./main"] 