package apputil

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"strings"
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
}

func FileExists(filepath string) bool {
	fileinfo, err := os.Stat(filepath)
	if err != nil {
		return false
	}
	if fileinfo.IsDir() {
		return false
	}
	return true
}
func FilenameWithoutExtension(fn string) string { return strings.TrimSuffix(fn, path.Ext(fn)) }

func DirExists(filepath string) bool {
	fileinfo, err := os.Stat(filepath)
	if err != nil {
		return false
	}
	if fileinfo.IsDir() {
		return true
	}
	return false
}
