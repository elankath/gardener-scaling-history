package apputil

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func WaitForSignalAndShutdown(cancelFunc context.CancelFunc) {
	slog.Info("Waiting until quit...")
	quit := make(chan os.Signal, 1)

	/// Use signal.Notify() to listen for incoming SIGINT and SIGTERM signals and relay them to the quit channel.
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)
	s := <-quit
	slog.Warn("Cleanup and Exit!", "signal", s.String())
	cancelFunc()
	//TODO: Clean up and exit code here.

}
