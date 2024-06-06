package reporter

import (
	"github.com/elankath/scalehist"
	"io"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"
)

type defaultReporter struct {
	ReportDir string
}

func (d defaultReporter) GenerateTextReport(analysis scalehist.Analysis) (reportPath string, err error) {
	reportPath = path.Join(d.ReportDir, analysis.Name+".txt")
	file, err := os.Create(reportPath)
	if err != nil {
		return "", err
	}
	defer file.Close() //TODO handle error here
	for _, s := range analysis.Scenarios {
		var sb strings.Builder

		sb.WriteString("------\n")
		sb.WriteString("##ScaleUpEvents\n")
		for _, event := range s.ScaleUpEvents {
			sb.WriteString(event.String() + "\n")
		}
		sb.WriteString("##UnscheduledPods\n")
		for _, p := range s.UnscheduledPods {
			sb.WriteString(p.String() + "\n")
		}
		sb.WriteString("##NodeGroups\n")
		for _, ng := range s.NodeGroups {
			sb.WriteString(ng.String() + "\n")
		}
		sb.WriteString("##CASettingsInfo\n")
		sb.WriteString("Expander : " + s.CASettings.Expander + "\n")
		sb.WriteString("Priorities : " + s.CASettings.Priorities + "\n")
		sb.WriteString("MaxNodesTotal : " + strconv.Itoa(s.CASettings.MaxNodesTotal) + "\n")
		sb.WriteString("------\n")
		scenarioStr := sb.String()
		slog.Info("reported scenario", "scenario", scenarioStr)
		_, err = io.WriteString(file, scenarioStr)
		if err != nil {
			return
		}
	}
	return
}

//
//func mergeScaleUpNodeGroupsWithAll(allNGs []scalehist.NodeGroupInfo, scaledUpNGs []scalehist.NodeGroupInfo) []scalehist.NodeGroupInfo {
//	nodeGroupName := make(map[string]scalehist.NodeGroupInfo)
//	for _, ng := range scaledUpNGs {
//		nodeGroupName[ng.Name] = ng
//	}
//	for _, ng := range allNGs {
//		_, ok := nodeGroupName[ng.Name]
//		if !ok {
//			ng.TargetSize = ng.CurrentSize
//			nodeGroupName[ng.Name] = ng
//		}
//	}
//
//	return maps.Values(nodeGroupName)
//}
//
//func getNodePoolFromNodeGroups(nodeGroups []scalehist.NodeGroupInfo) []api.NodePool {
//	nodePooltoNodeGroupsMap := make(map[string][]scalehist.NodeGroupInfo)
//	for _, ng := range nodeGroups {
//		nodePooltoNodeGroupsMap[ng.PoolName] = append(nodePooltoNodeGroupsMap[ng.PoolName], ng)
//	}
//	var nodePools []api.NodePool
//	for name, nodeGroups := range nodePooltoNodeGroupsMap {
//		nodePool := api.NodePool{}
//		for _, ng := range nodeGroups {
//			nodePool.Name = name
//			nodePool.Zones = append(nodePool.Zones, ng.Zone)
//			nodePool.Max = int32(ng.PoolMax)
//			nodePool.Current += int32(ng.CurrentSize)
//			nodePool.ScaleUp += int32(ng.TargetSize - ng.CurrentSize)
//			nodePool.InstanceType = ng.MachineType
//		}
//		nodePools = append(nodePools, nodePool)
//	}
//	return nodePools
//}
//
//func parseNodeInfo(nodes []scalehist.NodeInfo) []api.NodeInfo {
//	return lo.Map(nodes, func(node scalehist.NodeInfo, _ int) api.NodeInfo {
//		return api.NodeInfo{
//			Name:        node.Name,
//			Labels:      node.Labels,
//			Taints:      node.Taints,
//			Allocatable: node.Allocatable,
//			Capacity:    node.Capacity,
//		}
//
//	})
//}
//
//func parsePodInfo(pods []scalehist.PodInfo) []api.PodInfo {
//	return lo.Map(pods, func(pod scalehist.PodInfo, _ int) api.PodInfo {
//		return api.PodInfo{
//			NamePrefix: pod.Name,
//			Labels:     pod.Labels,
//			Requests:   pod.Requests,
//			//TODO: Confused
//			//Spec:       pod.Spec,
//			Count: 1,
//		}
//	})
//}
//
//func parseCaSettings(caSettings scalehist.CASettingsInfo) api.CASettingsInfo {
//	return api.CASettingsInfo{
//		Expander:      caSettings.Expander,
//		MaxNodesTotal: caSettings.MaxNodesTotal,
//		Priorities:    caSettings.Priorities,
//	}
//}
//func (d defaultReporter) GenerateSimulationRequests(analysis scalehist.Analysis) (requests []api.SimulationRequest) {
//	for _, s := range analysis.Scenarios {
//		simreq := api.SimulationRequest{
//			ID: string(uuid.NewUUID()),
//		}
//		nodeGroups := mergeScaleUpNodeGroupsWithAll(s.NodeGroups, s.ScaledUpNodeGroups)
//		nodePools := getNodePoolFromNodeGroups(nodeGroups)
//		simreq.NodePools = nodePools
//		simreq.Nodes = parseNodeInfo(s.Nodes)
//		simreq.UnscheduledPods = parsePodInfo(s.UnscheduledPods)
//		simreq.ScheduledPods = parsePodInfo(s.ScheduledPods)
//		simreq.CaSettings = parseCaSettings(s.CASettings)
//		requests = append(requests, simreq)
//	}
//	return
//}

func (d defaultReporter) GenerateJsonReport(analysis scalehist.Analysis) (reportPath string, err error) {
	reportPath = path.Join(d.ReportDir, analysis.Name+".json")
	file, err := os.Create(reportPath)
	if err != nil {
		return "", err
	}
	defer file.Close() //TODO handle error here
	requestsJSON, err := json.Marshal(analysis)
	if err != nil {
		return
	}
	_, err = io.WriteString(file, string(requestsJSON))
	if err != nil {
		return
	}
	slog.Info("successfully generated json report", "reportPath", reportPath)
	return
}

func NewReporter(reportDir string) (scalehist.Reporter, error) {
	return &defaultReporter{ReportDir: reportDir}, nil
}

func AnalyzerAndGenerateTextReport() error {
	panic("implement me")
}
