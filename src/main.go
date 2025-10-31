package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/coordinator-service/src/config"
	"github.com/coordinator-service/src/server"
)

func main() {
	config := loadConfig()
	setupLogging(config)

	srv, err := server.NewServer(config)
	if err != nil {
		slog.Error("Failed to initialize server", "error", err)
		os.Exit(1)
	}

	startServiceWithGracefulShutdown(srv)
}

func loadConfig() config.GlobalConfig {
	config, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	return config
}

func setupLogging(config config.GlobalConfig) {
	// Set log level from configuration
	logLevel := slog.LevelInfo
	switch config.GetLogLevel() {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)
}

func startServiceWithGracefulShutdown(srv *server.Server) {
	// Channel to listen for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		srv.Start()
		slog.Info("Coordinator server started successfully")
	}()

	// Wait for interrupt signal
	<-quit
	slog.Info("Shutting down service...")

	// Attempt graceful shutdown
	srv.Stop()
	slog.Info("Service exited gracefully")
}
