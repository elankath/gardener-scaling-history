package apputil

import (
	"cmp"
	"context"
	gst "github.com/elankath/gardener-scaling-types"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"slices"
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

func FilenameWithoutExtension(fp string) string {
	fn := path.Base(fp)
	return strings.TrimSuffix(fn, path.Ext(fn))
}

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

// SortPodsForReadability sorts the given podInfos so that application unscheduled pods appear first in the slice.
func SortPodsForReadability(podInfos []gst.PodInfo) {
	slices.SortFunc(podInfos, func(a, b gst.PodInfo) int {
		s1 := a.PodScheduleStatus
		s2 := b.PodScheduleStatus
		if s1 == s2 {
			return cmp.Compare(a.Name, b.Name)
		}
		if s1 == gst.PodUnscheduled {
			return -1
		}
		if s2 == gst.PodUnscheduled {
			return 1
		}
		return cmp.Compare(s1, s2)
	})

}
