package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/server"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/logger"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize logger
	logger.Init(cfg.LogLevel())

	// Check for MCP stdio mode — when enabled, the server runs as a
	// subprocess serving the MCP protocol over stdin/stdout (for Claude
	// Desktop and other local MCP clients). This bypasses the normal
	// HTTP server startup.
	if cfg.MCPServer.Stdio {
		slog.Info("Starting in MCP stdio mode (subprocess for local MCP clients)")
		srv, err := server.New(cfg)
		if err != nil {
			slog.Error("failed to create server", "error", err)
			os.Exit(1)
		}

		ctx, cancel := signal.NotifyContext(context.Background(),
			syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		srv.StartMCPServerStdio(ctx)
		slog.Info("MCP stdio server stopped")
		return
	}

	slog.Info("Writing Agent V2 starting...",
		"port", cfg.Server.Port,
		"llm_configured", cfg.DeepSeek.APIKey != "",
	)

	// Create server
	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	// Handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start server
	if err := srv.Start(ctx); err != nil && err != context.Canceled {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}
