package main

import (
	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/replayer"
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
		slog.Info("ScalingEvent", "eventIndex", strconv.Itoa(i), "eventTimeUnixNanos", e.EventTime.UTC().UnixNano(), "reason", e.Reason, "msg", e.Message)
	}
	var replayEvent gsc.EventInfo
	var eventIndex int
	for {
		eventIndex, replayEvent = replayer.GetNextReplayEvent(scalingEvents, eventIndex)
		if eventIndex == -1 {
			slog.Info("No more scaling events")
			break
		}
		slog.Info("ReplayScalingEvent", "eventIndex", eventIndex, "eventTimeUnixNanos", replayEvent.EventTime.UTC().UnixNano(), "reason", replayEvent.Reason, "msg", replayEvent.Message)
		/*
			1725101735	e5b611b9-562c-4a84-8766-702875ff2a34	2024-08-31 10:55:35+00:00	TriggeredScaleUp	pod triggered scale-up: [{shoot--hc-eu30--prod-gc-haas-default-z2 7->8 (max: 200)}]	thanos-compactor-6c9c7dbcc9-bv992
			1725144986	dffd240b-4cba-4307-92a7-fda5f40fde89	2024-08-31 22:56:26+00:00	TriggeredScaleUp	pod triggered scale-up: [{shoot--hc-eu30--prod-gc-haas-default-z2 7->8 (max: 200)}]	thanos-compactor-6c9c7dbcc9-wt9dn
			1725188247	311ca729-b350-4362-a5d9-46b640a83e7b	2024-09-01 10:57:27+00:00	TriggeredScaleUp	pod triggered scale-up: [{shoot--hc-eu30--prod-gc-haas-default-z2 7->8 (max: 200)}]	thanos-compactor-6c9c7dbcc9-8rpd4

		*/
	}
	if true {
		os.Exit(0)
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
