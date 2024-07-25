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

// SortPodInfosForReadability sorts the given podInfos so that application pods and unscheduled pods appear first in the slice.
func SortPodInfosForReadability(podInfos []gsc.PodInfo) {
	slices.SortFunc(podInfos, func(a, b gsc.PodInfo) int {
		s1 := a.PodScheduleStatus
		s2 := b.PodScheduleStatus

		n1 := a.Name
		n2 := b.Name
		ns1 := a.Namespace
		ns2 := b.Namespace

		if ns1 == "kube-system" && ns1 != ns2 {
			return 1
		}
		if ns2 == "kube-system" && ns2 != ns1 {
			return -1
		}
		if s1 == gsc.PodUnscheduled {
			return -1
		}
		if s2 == gsc.PodUnscheduled {
			return 1
		}
		return cmp.Compare(n1, n2)
	})
}

// SortPodInfoForDeployment sorts the given podInfos so that kube-system and higher priority pods are sorted first.
func SortPodInfoForDeployment(a, b gsc.PodInfo) int {
	ns1 := a.Namespace
	ns2 := b.Namespace
	s1 := a.PodScheduleStatus
	s2 := b.PodScheduleStatus
	p1 := a.Spec.Priority
	p2 := b.Spec.Priority
	c1 := a.CreationTimestamp
	c2 := b.CreationTimestamp

	if ns1 == "kube-system" && ns1 != ns2 {
		return -1
	}
	if ns2 == "kube-system" && ns2 != ns1 {
		return 1
	}

	if s1 == gsc.PodScheduleCommited && s1 != s2 {
		return -1
	}

	if s2 == gsc.PodScheduleCommited && s2 != s1 {
		return 1
	}

	if p1 != nil && p2 != nil {
		// higher priority must come earlier
		return cmp.Compare(*p2, *p1)
	}
	return cmp.Compare(c1.UnixMilli(), c2.UnixMilli())
}

//func ListAllNodes(ctx context.Context, clientSet *kubernetes.Clientset)
