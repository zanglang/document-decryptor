// Command document-decryptor runs a small HTTP service that accepts an
// encrypted PDF plus a list of identifying strings, selects the correct
// password based on configured substring matches, decrypts the PDF using
// qpdf, and returns the decrypted PDF.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/zanglang/document-decryptor/internal"
)

const (
	defaultListenAddr  = ":8080"
	defaultConfigPath  = "/config/patterns.json"
	defaultMaxUpload   = 10 * 1024 * 1024 // 10 MiB
	defaultQPDFTimeout = 30 * time.Second
	defaultQPDFBin     = "qpdf"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	listenAddr := getEnv("LISTEN_ADDR", defaultListenAddr)
	configPath := getEnv("CONFIG_PATH", defaultConfigPath)
	qpdfBin := getEnv("QPDF_BIN", defaultQPDFBin)
	maxUploadBytes := getEnvInt64("MAX_UPLOAD_BYTES", defaultMaxUpload)
	qpdfTimeout := getEnvSeconds("QPDF_TIMEOUT_SECONDS", defaultQPDFTimeout)

	decryptor := &internal.QPDFDecryptor{
		BinPath: qpdfBin,
		Timeout: qpdfTimeout,
		Logger:  logger,
	}

	srv := internal.NewServer(configPath, maxUploadBytes, decryptor, logger)

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      qpdfTimeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	logger.Info("starting document-decryptor",
		"listen_addr", listenAddr,
		"config_path", configPath,
		"max_upload_bytes", maxUploadBytes,
		"qpdf_timeout", qpdfTimeout.String(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections")
		stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
		logger.Info("shutdown complete")
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil || parsed <= 0 {
		slog.Warn("invalid environment value, using default", "key", key, "value", v)
		return def
	}
	return parsed
}

func getEnvSeconds(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed <= 0 {
		slog.Warn("invalid environment value, using default", "key", key, "value", v)
		return def
	}
	return time.Duration(parsed) * time.Second
}
