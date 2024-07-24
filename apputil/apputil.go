package apputil

import (
	"cmp"
	"context"
	"github.com/elankath/gardener-scaling-common"
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
func SortPodsForReadability(podInfos []gsc.PodInfo) {
	slices.SortFunc(podInfos, func(a, b gsc.PodInfo) int {
		s1 := a.PodScheduleStatus
		s2 := b.PodScheduleStatus
		if s1 == gsc.PodUnscheduled {
			return -1
		}
		if s2 == gsc.PodUnscheduled {
			return 1
		}
		return cmp.Compare(s1, s2)
	})
}

//func ListAllNodes(ctx context.Context, clientSet *kubernetes.Clientset)
