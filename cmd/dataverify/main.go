package main

import (
	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-history/db"
	"log/slog"
	"os"
	"slices"
	"strconv"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if len(dbPath) == 0 {
		slog.Error("DB_PATH env must be set for dataverify tool")
		os.Exit(1)
	}
	slog.Info("Creating dataAccess.", "dbPath", dbPath)
	dataAccess := db.NewDataAccess(dbPath)
	err := dataAccess.Init()
	if err != nil {
		slog.Error("could not init dataAccess.", "error", err)
		os.Exit(2)
	}
	defer dataAccess.Close()
	scalingEvents, err := dataAccess.LoadTriggeredScaleUpEvents()
	if len(scalingEvents) == 0 {
		slog.Warn("no scaling events in db.", "dbPath", dbPath)
		return
	}
	for i, e := range scalingEvents {
		slog.Info(strconv.Itoa(i), "eventTimeUnixNanos", e.EventTime.UTC().UnixNano(), "reason", e.Reason, "msg", e.Message)
	}
	if true {
		return
	}
	markTime := scalingEvents[0].EventTime.UTC()
	markTimeNano := markTime.UnixNano()
	slog.Info("Choosing markTime from scaling event.", "markTime", markTime, "markTimeNano", markTimeNano, "event", scalingEvents[0])
	nodes, err := dataAccess.LoadNodeInfosBefore(markTime)
	if err != nil {
		return
	}
	slices.SortFunc(nodes, func(a, b gsc.NodeInfo) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp)
	})
	for _, n := range nodes {
		slog.Info("node", "nodeName", n.Name, "nodeSnapshotTimestamp", n.SnapshotTimestamp, "markTime", markTime, "markTimeNano", markTimeNano)
	}
	pods, err := dataAccess.GetLatestPodInfosBeforeSnapshotTimestamp(markTime)
	if err != nil {
		return
	}
	for _, p := range pods {
		podDeleted := !p.DeletionTimestamp.IsZero()
		slog.Info("pod info.", "podName", p.Name, "podCreationTimestamp", p.CreationTimestamp, "podSnapshotTimestamp", p.SnapshotTimestamp, "podRequests", gsc.ResourcesAsString(p.Requests))
		if podDeleted {
			slog.Warn("pod deleted!", "podName", p.Name)
		}
	}
	slog.Info("Loaded nodes.", "#nodes", len(nodes), "markTime", markTime, "markTimeNano", markTimeNano)
	slog.Info("Loaded pods.", "#pods", len(pods))
	slog.Info("Finished dump.", "markTime", markTime, "markTimeNano", markTimeNano)
}
