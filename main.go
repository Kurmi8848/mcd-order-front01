package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var logger *log.Logger

func initLogger() {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	logFile, err := os.OpenFile(filepath.Join(logDir, "order.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}

	// 標準出力とファイル出力のマルチライターを設定
	mw := io.MultiWriter(os.Stdout, logFile)
	logger = log.New(mw, "", log.LstdFlags)
}

func main() {
	initLogger()
	logger.Println("[INIT] Application starting...")

	// DB初期化
	initDB()
	defer closeDB()

	// Go Mux (Go 1.22+) を使用したルーティング設定
	mux := http.NewServeMux()

	// 注文管理機能
	mux.HandleFunc("POST /api/orders", handleCreateOrder)
	mux.HandleFunc("GET /api/orders", handleListOrders)
	mux.HandleFunc("GET /api/orders/{orderNo}", handleGetOrder)
	mux.HandleFunc("PUT /api/orders/{orderNo}/status", handleUpdateOrderStatus)

	// フロント掲示板機能・厨房機能
	mux.HandleFunc("POST /api/board", handleBoard)
	mux.HandleFunc("POST /api/kitchen", handleKitchen)

	// 全体のミドルウェア適用 (CORS & 共通ロギング)
	wrappedHandler := corsMiddleware(loggingMiddleware(mux))

	server := &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: wrappedHandler,
	}

	// グレースフルシャットダウンの制御
	go func() {
		logger.Printf("[START] Server listening on %s\n", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("[ERROR] Serve error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Println("[SHUTDOWN] Shutting down server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Fatalf("[ERROR] Server forced to shutdown: %v", err)
	}

	logger.Println("[SHUTDOWN] Server stopped safely.")
}