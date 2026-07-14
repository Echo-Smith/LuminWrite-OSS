package logger

import (
	"log/slog"
	"os"
)

// Init initializes the global slog logger with JSON output.
func Init(level slog.Level) {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	slog.SetDefault(slog.New(handler))
}
