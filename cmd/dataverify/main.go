package main

import (
	"cmp"
	"fmt"
	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-common/clientutil"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/replayer"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"time"
)

const podLimitOnNode = 70

func main() {
	var err error
	var replayEvent gsc.EventInfo
	var eventIndex, snapshotNumber int
	var nodeInfos []gsc.NodeInfo
	//var podInfos []gsc.PodInfo
	var loadedNodeInfos []gsc.NodeInfo
	var runMarkTime, runPrevMarkTime time.Time
	var snapshotID string
	var clusterSnapshot gsc.ClusterSnapshot

	dbPath := os.Getenv("DB_PATH")
	if len(dbPath) == 0 {
		slog.Error("DB_PATH env must be set for dataverify tool")
		os.Exit(1)
	}
	slog.Info("Creating dataAccess.", "dbPath", dbPath)
	dataAccess := db.NewDataAccess(dbPath)
	err = dataAccess.Init()
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
		slog.Info("ScalingEvent", "eventIndex", strconv.Itoa(i), "eventTimeUnixMicros", e.EventTime.UTC().UnixMicro(), "reason", e.Reason, "msg", e.Message)
	}
	fmt.Println()
	fmt.Println()
	for {
		eventIndex, replayEvent = replayer.GetNextReplayEvent(scalingEvents, eventIndex)
		if eventIndex == -1 {
			slog.Info("No more scaling events")
			break
		}
		runMarkTime = replayEvent.EventTime.UTC()
		runMarkTimeMicros := runMarkTime.UnixMicro()
		slog.Info("ReplayScalingEvent", "eventIndex", eventIndex, "eventTime", runMarkTime, "eventTimeUnixMicros", runMarkTimeMicros, "reason", replayEvent.Reason, "msg", replayEvent.Message)
		nodeInfos, err = dataAccess.LoadNodeInfosBefore(replayEvent.EventTime.UTC())
		if err != nil {
			slog.Error("cant load nodeInfos.", "error", err)
			return
		}
		//nodeInfosByName = lo.G(nodeInfos, apputil.GetNodeName)
		snapshotNumber++
		snapshotID = fmt.Sprintf("cs-%d", snapshotNumber)
		clusterSnapshot, err = replayer.GetRecordedClusterSnapshot(dataAccess, snapshotNumber, snapshotID, runPrevMarkTime, runMarkTime)
		if err != nil {
			slog.Error("failed to GetRecordedClusterSnapshot", "error", err, "runPrevMarkTime", runPrevMarkTime, "runMarkTime", runMarkTime)
			return
		}
		podsByNodeName := lo.GroupBy(clusterSnapshot.Pods, func(p gsc.PodInfo) string {
			return p.Spec.NodeName
		})

		for nodeName, podsOnNode := range podsByNodeName {
			numPodsOnNode := len(podsOnNode)
			if numPodsOnNode <= podLimitOnNode {
				continue
			}
			//			slog.Info("Node has more than pod limit.", "nodeName", nodeName, "numPodsOnNode", numPodsOnNode, "podLimitOnNode", podLimitOnNode, "eventIndex", eventIndex, "runMarkTime", runMarkTime, "runMarkTimeMicros", runMarkTimeMicros)
			loadedNode, ok := lo.Find(nodeInfos, apputil.NodeHasMatchingName(nodeName))
			if ok {
				loadedNodeInfos = append(loadedNodeInfos, loadedNode)
			} else {
				slog.Error("Could not find loaded node", "nodeName", nodeName)
			}
		}
		if len(loadedNodeInfos) == 0 {
			slog.Info("There are no loadedNodeInfos")
		} else {
			slog.Info("Loaded nodeInfos", "numLoadedNodeInfos", len(loadedNodeInfos))
		}

		slices.SortFunc(loadedNodeInfos, func(a, b gsc.NodeInfo) int {
			aNumPods := len(podsByNodeName[a.Name])
			bNumPods := len(podsByNodeName[a.Name])
			return cmp.Compare(bNumPods, aNumPods)
		})

		for _, loadedNodeInfo := range loadedNodeInfos {
			podInfosOnNode := podsByNodeName[loadedNodeInfo.Name]
			var podSpecsOnNode []corev1.PodSpec
			for _, podInfo := range podInfosOnNode {
				podSpecsOnNode = append(podSpecsOnNode, podInfo.Spec)
			}
			totalPodRequestsOnNode := clientutil.SumResourceRequest(podSpecsOnNode)
			slog.Info("Loaded Node Details.",
				"snapshotNumber", snapshotNumber,
				"runMarkTime", runMarkTime,
				"runPrevMarkTime", runPrevMarkTime,
				"numPods", len(podInfosOnNode),
				"nodeName", loadedNodeInfo.Name,
				"totalPodRequestsOnNode", gsc.ResourcesAsString(totalPodRequestsOnNode),
				"nodeCapacity", gsc.ResourcesAsString(loadedNodeInfo.Capacity),
				"nodeAllocatable", gsc.ResourcesAsString(loadedNodeInfo.Allocatable))
		}
		runPrevMarkTime = runMarkTime
	}
}
