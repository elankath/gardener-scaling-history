package main

import (
	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-common/clientutil"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/replayer"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"log/slog"
	"os"
	"strconv"
)

const podLimitOnNode = 25

func main() {
	var err error
	var replayEvent gsc.EventInfo
	var eventIndex int
	var nodeInfos []gsc.NodeInfo
	var podInfos []gsc.PodInfo
	var loadedNodeInfos []gsc.NodeInfo

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
	for {
		eventIndex, replayEvent = replayer.GetNextReplayEvent(scalingEvents, eventIndex)
		if eventIndex == -1 {
			slog.Info("No more scaling events")
			break
		}
		replayMarkTime := replayEvent.EventTime.UTC()
		replayMarkTimeMicros := replayMarkTime.UnixMicro()
		slog.Info("ReplayScalingEvent", "eventIndex", eventIndex, "eventTime", replayMarkTime, "eventTimeUnixMicros", replayMarkTimeMicros, "reason", replayEvent.Reason, "msg", replayEvent.Message)
		nodeInfos, err = dataAccess.LoadNodeInfosBefore(replayEvent.EventTime.UTC())
		if err != nil {
			slog.Error("cant load nodeInfos.", "error", err)
			return
		}
		//nodeInfosByName = lo.G(nodeInfos, apputil.GetNodeName)
		podInfos, err = dataAccess.GetLatestPodInfosBeforeSnapshotTimestamp(replayMarkTime)
		podInfos = lo.Filter(podInfos, func(item gsc.PodInfo, index int) bool {
			return item.Namespace != "kube-system"
		})
		podsByNodeName := lo.GroupBy(podInfos, func(p gsc.PodInfo) string {
			return p.Spec.NodeName
		})

		for nodeName, podsOnNode := range podsByNodeName {
			numPodsOnNode := len(podsOnNode)
			if numPodsOnNode < podLimitOnNode {
				continue
			}
			slog.Info("Node has more than pod limit.", "nodeName", nodeName, "numPodsOnNode", numPodsOnNode, "podLimitOnNode", podLimitOnNode, "eventIndex", eventIndex, "replayMarkTime", replayMarkTime, "replayMarkTimeMicros", replayMarkTimeMicros)
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

		for _, loadedNodeInfo := range loadedNodeInfos {
			if loadedNodeInfo.Name == "ip-10-250-0-172.eu-central-1.compute.internal" {
				podInfosOnNode := podsByNodeName[loadedNodeInfo.Name]
				var podSpecsOnNode []corev1.PodSpec
				for _, podInfo := range podInfosOnNode {
					podSpecsOnNode = append(podSpecsOnNode, podInfo.Spec)
				}
				totalPodRequestsOnNode := clientutil.SumResourceRequest(podSpecsOnNode)
				slog.Info("Node totalPodRequestsOnNode  ", "nodeName", loadedNodeInfo.Name, "totalPodRequestsOnNode", gsc.ResourcesAsString(totalPodRequestsOnNode))
				slog.Info("Node Capacity And Allocatable", "nodeName", loadedNodeInfo.Name, "nodeCapacity", gsc.ResourcesAsString(loadedNodeInfo.Capacity), "nodeAllocatable", gsc.ResourcesAsString(loadedNodeInfo.Allocatable))
				return
			}
		}
	}
}
