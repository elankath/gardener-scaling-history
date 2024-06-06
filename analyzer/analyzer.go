package analyzer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scalehist"
	"github.com/elankath/gardener-scalehist/db"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"
)

var ErrNoPodsBeforeEvent = errors.New("No pods for event")

type defaultAnalyzer struct {
	name                       string
	dataAccess                 *db.DataAccess
	scenarioCoalesceInterval   time.Duration
	scenarioTolerationInterval time.Duration
}

func (d *defaultAnalyzer) Close() error {
	if d.dataAccess != nil {
		return d.dataAccess.Close()
	}
	return nil
}

func filterUnscheduledPodsForScenario(unscheduledPods []scalehist.PodInfo, scheduledPodUIDs []string) []scalehist.PodInfo {
	return lo.Filter(unscheduledPods, func(unscheduledPod scalehist.PodInfo, index int) bool {
		isPodScheduled := slices.ContainsFunc(scheduledPodUIDs, func(s string) bool {
			if s == unscheduledPod.UID {
				return true
			}
			return false
		})
		return !isPodScheduled
	})
}

func (d *defaultAnalyzer) SegregatePods(eventTime time.Time) (unscheduledPods, nominatePods, scheduledPods []scalehist.PodInfo, err error) {
	pods, err := d.dataAccess.GetLatestPodsBeforeTimestamp(eventTime)
	if err != nil {
		return
	}

	seenMap := make(map[string]struct{})
	for _, pod := range pods {
		_, ok := seenMap[pod.UID]
		if ok {
			continue
		}
		seenMap[pod.UID] = struct{}{}
		if pod.PodScheduleStatus == scalehist.PodUnscheduled {
			unscheduledPods = append(unscheduledPods, pod)
			continue
		}
		if pod.PodScheduleStatus == scalehist.PodScheduleNominated {
			nominatePods = append(nominatePods, pod)
			continue
		}
		if pod.PodScheduleStatus == scalehist.PodScheduleCommited {
			scheduledPods = append(scheduledPods, pod)
			continue
		}
	}
	// Since there is a case where a Pod is first Unscheduled and then becomes Scheduled,
	// we should remove from the unscheduledPods slice if it is present in the scheduledPod slice

	return

	//scheduledPodUIDs := getScheduledPodUIDsInEventsBefore(events, eventTime) // FIXME, TODO: Use UID's here.
	//scheduledPods, err = d.dataAccess.GetLatestScheduledPodsBeforeTimestamp(eventTime)
	//unscheduledPods = filterUnscheduledPodsForScenario(pods, scheduledPodUIDs)
	//if len(unscheduledPods) == 0 || len(scheduledPods) == 0 {
	//	err = ErrNoPodsBeforeEvent
	//	return
	//}
	//isUnscheduledMap := lo.SliceToMap(unscheduledPods, func(item scalehist.PodInfo) (string, struct{}) {
	//	return item.UID, struct{}{}
	//})
	//scheduledPods = lo.Filter(scheduledPods, func(item scalehist.PodInfo, index int) bool {
	//	_, ok := isUnscheduledMap[item.UID]
	//	return !ok
	//})
	//return
}

func (d *defaultAnalyzer) ComputeCandidateScenarios(events []scalehist.EventInfo) ([]scalehist.Scenario, error) {
	triggeredScaleUpEvents := getTriggeredScaleUpEvents(events)

	var candidateScenarios []scalehist.Scenario
	var prevScenarioEndTime time.Time
	for _, t := range triggeredScaleUpEvents {
		unscheduledPods, nominatedPods, scheduledPods, err := d.SegregatePods(t.EventTime)
		if err != nil {
			if errors.Is(err, ErrNoPodsBeforeEvent) {
				slog.Warn("cannot compute unscheduled and scheduled pods for event", "error", err, "event", t)
			} else {
				slog.Error("cannot compute unscheduled and scheduled pods for event", "error", err, "event", t)
			}
			continue
		}
		if len(unscheduledPods) == 0 {
			slog.Warn("no unscheduled pods available for the triggered event", "event.UID", t.UID, "event.Time", t.EventTime)
			continue
		}
		criticalComponentRequests, systemComponentRequests, err := computeMaxSystemAndCriticalComponentsRequests(scheduledPods)
		if err != nil {
			return nil, err
		}

		slices.SortFunc(unscheduledPods, cmpPodInfoByCreationTimestamp)

		scheduledPods = lo.Filter(scheduledPods, func(item scalehist.PodInfo, index int) bool {
			return item.Namespace != "kube-system"
		})

		nodeGroups, err := d.GetScenarioNodeGroupInfos(prevScenarioEndTime, t.EventTime)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(scheduledPods, cmpPodInfoByCreationTimestamp)
		scenario := scalehist.Scenario{
			StartTime:                 t.EventTime,
			EndTime:                   t.EventTime,
			CriticalComponentRequests: criticalComponentRequests,
			SystemComponentRequests:   systemComponentRequests,
			UnscheduledPods:           unscheduledPods,
			NominatedPods:             nominatedPods,
			ScheduledPods:             scheduledPods,
			ScaleUpEvents:             []scalehist.EventInfo{t},
			NodeGroups:                nodeGroups,
		}

		candidateScenarios = append(candidateScenarios, scenario)
		prevScenarioEndTime = t.EventTime
	}
	if len(candidateScenarios) == 0 {
		return nil, fmt.Errorf("no candidate-scenarios in db: please run recorder, trigger scaleup, wait for cluster to stabilize, then grab the generated db")
	}
	return candidateScenarios, nil
}

func getPodInfoCompare(pods []scalehist.PodInfo) []scalehist.PodInfoKey {
	return lo.Map(pods, func(pod scalehist.PodInfo, index int) scalehist.PodInfoKey {
		return scalehist.PodInfoKey{
			UID:  pod.UID,
			Name: pod.Name,
			Hash: pod.Hash,
		}
	})
}

//type scenarioExtended struct {
//	scalehist.Scenario
//	unscheduledPodKeys []scalehist.PodInfoKey
//	scheduledPodKeys   []scalehist.PodInfoKey
//}

func areNodeGroupsCoalescable(nG1, nG2 []scalehist.NodeGroupInfo) bool {
	ng1ShootMax := slices.MaxFunc(nG1, func(a, b scalehist.NodeGroupInfo) int {
		return cmp.Compare(a.ShootGeneration, b.ShootGeneration)
	})
	ng2ShootMax := slices.MaxFunc(nG2, func(a, b scalehist.NodeGroupInfo) int {
		return cmp.Compare(a.ShootGeneration, b.ShootGeneration)
	})
	if len(nG1) != len(nG2) && ng1ShootMax.ShootGeneration == ng2ShootMax.ShootGeneration {
		return true
	}
	return ng1ShootMax.ShootGeneration == ng2ShootMax.ShootGeneration
	//ng1PoolMax := slices.MaxFunc(nG1, func(a, b scalehist.NodeGroupInfo) int {
	//	return cmp.Compare(a.PoolMax, b.PoolMax)
	//})
	//ng2PoolMax := slices.MaxFunc(nG2, func(a, b scalehist.NodeGroupInfo) int {
	//	return cmp.Compare(a.PoolMax, b.PoolMax)
	//})
	//if ng1PoolMax != ng2PoolMax {
	//	return false
	//}
	//ng1PoolMin := slices.MinFunc(nG1, func(a, b scalehist.NodeGroupInfo) int {
	//	return cmp.Compare(a.PoolMin, b.PoolMin)
	//})
	//ng2PoolMin := slices.MinFunc(nG1, func(a, b scalehist.NodeGroupInfo) int {
	//	return cmp.Compare(a.PoolMin, b.PoolMin)
	//})
	//return ng1PoolMin != ng2PoolMin
}

func canCoalesce(prevScenario, scenario scalehist.Scenario, scenarioDivideInterval time.Duration) bool {
	//if len(prevScenario.UnscheduledPods) == 0 || len(scenario.UnscheduledPods) == 0 {
	//	return true
	//}
	if prevScenario.CASettings.Hash != scenario.CASettings.Hash {
		return false
	}
	//lastUnscheduledPodPrevScenario := prevScenario.UnscheduledPods[len(prevScenario.UnscheduledPods)-1]
	//firstUnscheduledPodScenario := scenario.UnscheduledPods[0]
	//
	//if lastUnscheduledPodPrevScenario.CreationTimestamp.Add(scenarioCoalesceInterval).Before(firstUnscheduledPodScenario.CreationTimestamp) {
	//	return false
	//}
	if prevScenario.EndTime.Add(scenarioDivideInterval).Before(scenario.StartTime) {
		return false
	}

	return areNodeGroupsCoalescable(prevScenario.NodeGroups, scenario.NodeGroups)
}

func UniquePodInfos(pods []scalehist.PodInfo) []scalehist.PodInfo {
	slices.SortFunc(pods, func(a, b scalehist.PodInfo) int {
		return strings.Compare(a.UID, b.UID)
	})
	pods = slices.CompactFunc(pods, func(a scalehist.PodInfo, b scalehist.PodInfo) bool {
		return a.UID == b.UID
	})
	slices.SortFunc(pods, cmpPodInfoByCreationTimestamp)
	return pods
}

func UniqueNodeGroupInfos(nodeGroups []scalehist.NodeGroupInfo) []scalehist.NodeGroupInfo {
	slices.SortFunc(nodeGroups, func(a, b scalehist.NodeGroupInfo) int {
		return strings.Compare(a.Hash, b.Hash)
	})
	nodeGroups = slices.CompactFunc(nodeGroups, func(a scalehist.NodeGroupInfo, b scalehist.NodeGroupInfo) bool {
		return a.Hash == b.Hash
	})
	slices.SortFunc(nodeGroups, func(a, b scalehist.NodeGroupInfo) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp)
	})
	return nodeGroups
}

func UniqueNodeInfos(nodes []scalehist.NodeInfo) []scalehist.NodeInfo {
	slices.SortFunc(nodes, func(a, b scalehist.NodeInfo) int {
		return strings.Compare(a.Hash, b.Hash)
	})
	nodes = slices.CompactFunc(nodes, func(a scalehist.NodeInfo, b scalehist.NodeInfo) bool {
		return a.Hash == b.Hash
	})
	slices.SortFunc(nodes, func(a, b scalehist.NodeInfo) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp)
	})
	return nodes
}

func CombineScenario(a, b scalehist.Scenario) (c scalehist.Scenario) {
	c.ScaleUpEvents = slices.Concat(a.ScaleUpEvents, b.ScaleUpEvents)
	//slices.SortFunc(c.ScaleUpEvents, cmpEventInfoByEventTimestamp)
	c.UnscheduledPods = UniquePodInfos(slices.Concat(a.UnscheduledPods, b.UnscheduledPods))
	c.ScheduledPods = UniquePodInfos(slices.Concat(a.ScheduledPods, b.ScheduledPods))
	//c.NodeGroups = UniqueNodeGroupInfos(slices.Concat(a.NodeGroups, b.NodeGroups))
	c.NodeGroups = CollapseNodeGroups(slices.Concat(a.NodeGroups, b.NodeGroups))
	c.Nodes = UniqueNodeInfos(slices.Concat(a.Nodes, b.Nodes))
	c.CriticalComponentRequests = ComputeMaximalResource(a.CriticalComponentRequests, b.CriticalComponentRequests)
	c.SystemComponentRequests = ComputeMaximalResource(a.SystemComponentRequests, b.SystemComponentRequests)
	c.CASettings = a.CASettings
	c.StartTime = a.StartTime
	if a.EndTime.Compare(b.EndTime) > 0 {
		c.EndTime = a.EndTime
	} else {
		c.EndTime = b.EndTime
	}
	// When combining across separate scale up events, you can have a  pod that was initially unscheduled in
	// scenario a and got scheduled after node scaled up. Then when you trigger scenario b, this pod will be in the scheduled pod list of b.
	// In such a circumstance, the pod will be repeated in both c.UnscheduledPods and c.ScheduledPods
	// we should remove it from c.ScheduledPods for proper combining.
	c.ScheduledPods = slices.DeleteFunc(c.ScheduledPods, func(info scalehist.PodInfo) bool {
		return scalehist.ContainsPod(info.UID, c.UnscheduledPods)
	})
	return
}

func CoalesceScenarios(scenarios []scalehist.Scenario, scenarioDivideInterval time.Duration) []scalehist.Scenario {

	if len(scenarios) <= 1 {
		return scenarios
	}
	//var prevScenario scalehist.Scenario
	var coalesceScenarios []scalehist.Scenario
	var coalesceScenarioIndex int
	for i, s := range scenarios {
		if i == 0 {
			//prevScenario = s
			coalesceScenarioIndex = 0
			coalesceScenarios = append(coalesceScenarios, s)
			continue
		}
		if canCoalesce(coalesceScenarios[coalesceScenarioIndex], s, scenarioDivideInterval) {
			//prevScenario.ScaleUpEvents = slices.Concat(prevScenario.ScaleUpEvents, s.ScaleUpEvents)
			coalesceScenarios[coalesceScenarioIndex] = CombineScenario(coalesceScenarios[coalesceScenarioIndex], s)
		} else {
			coalesceScenarioIndex++
			coalesceScenarios = append(coalesceScenarios, s)
		}

		//_, diff2 := lo.Difference(prevScenario.podKeys, s.podKeys)
		//if len(diff2) == 0 {
		//	prevScenario.ScaleUpEvents = slices.Concat(prevScenario.ScaleUpEvents, s.ScaleUpEvents)
		//} else {
		//	prevScenario = &s
		//	coalesceScenarios = append(coalesceScenarios, prevScenario)
		//}
	}

	return coalesceScenarios
}

func filterNodeGroupsWithEventUIDs(nodeGroups []db.NodeGroupWithEventUID, eventUIDs []string) []scalehist.NodeGroupInfo {
	presentMap := make(map[string]struct{})
	return lo.FilterMap(nodeGroups, func(item db.NodeGroupWithEventUID, _ int) (scalehist.NodeGroupInfo, bool) {
		isPresent := slices.ContainsFunc(eventUIDs, func(s string) bool {
			return s == item.EventUID
		})
		_, presentAlready := presentMap[item.Name]
		var nodeGroup scalehist.NodeGroupInfo
		if isPresent && !presentAlready {
			nodeGroup = item.NodeGroupInfo
			presentMap[item.Name] = struct{}{}
		}
		return nodeGroup, (isPresent && !presentAlready)
	})
}

func (d *defaultAnalyzer) PopulateScaledUpNodeGroups(scenarios []scalehist.Scenario) error {
	nodeGroupsExtended, err := d.dataAccess.LoadNodeGroupsWithEventUIDAndSameHash()
	if err != nil {
		return err
	}
	for i, s := range scenarios {
		eventUIDs := lo.Map(s.ScaleUpEvents, func(item scalehist.EventInfo, _ int) string {
			return item.UID
		})
		scaledUpNodeGroups := filterNodeGroupsWithEventUIDs(nodeGroupsExtended, eventUIDs)
		nodeGroupsMap := lo.SliceToMap(s.NodeGroups, func(item scalehist.NodeGroupInfo) (string, scalehist.NodeGroupInfo) {
			return item.Name, item
		})
		for _, ng := range scaledUpNodeGroups {
			nodeGroupsMap[ng.Name] = ng
		}
		scenarios[i].NodeGroups = maps.Values(nodeGroupsMap)
	}
	return nil
}

func getLatestNodeGroupsByName(startTime time.Time, nodeGroups []scalehist.NodeGroupInfo) (latestNodeGroups []scalehist.NodeGroupInfo) {
	latestNodeGroupsMap := make(map[string]scalehist.NodeGroupInfo)
	for _, ng := range nodeGroups {
		if ng.CreationTimestamp.After(startTime) {
			continue
		}
		if _, ok := latestNodeGroupsMap[ng.Name]; ok {
			continue
		}
		latestNodeGroupsMap[ng.Name] = ng
		latestNodeGroups = append(latestNodeGroups, ng)
	}
	slices.SortFunc(latestNodeGroups, func(a, b scalehist.NodeGroupInfo) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp)
	})
	return
}

func cmpNodeGroupsDescending(a, b scalehist.NodeGroupInfo) int {
	return b.CreationTimestamp.Compare(a.CreationTimestamp)
}

func CollapseNodeGroups(nodeGroups []scalehist.NodeGroupInfo) (collapsedNodeGroups []scalehist.NodeGroupInfo) {
	nodeGroupsByName := lo.GroupBy(nodeGroups, func(item scalehist.NodeGroupInfo) string {
		return item.Name
	})
	// NG1, NG2, NG3
	//NG4 - C - 4, T - 1
	//NG3 - C - 3 ,T - 4
	//NG2 - C - 2, T - 3
	//NG1 - C - 1, T - 2
	//NG0 - C - 2, T - 1
	for _, ngs := range nodeGroupsByName {
		slices.SortFunc(ngs, cmpNodeGroupsDescending)
		collapsedNodeGroup := ngs[0]
		for _, ng := range ngs {
			collapsedNodeGroup.TargetSize = max(collapsedNodeGroup.TargetSize, ng.TargetSize)
			collapsedNodeGroup.CurrentSize = min(collapsedNodeGroup.CurrentSize, ng.CurrentSize)
		}
		collapsedNodeGroup.Hash = collapsedNodeGroup.GetHash()
		collapsedNodeGroups = append(collapsedNodeGroups, collapsedNodeGroup)
	}
	return
}

func (d *defaultAnalyzer) GetScenarioNodeGroupInfos(prevScenarioEndTime, scenarioEndTime time.Time) (nodeGroups []scalehist.NodeGroupInfo, err error) {
	activeNodeGroups, err := d.dataAccess.LoadAllActiveNodeGroupsDesc()
	if err != nil {
		return
	}
	nodeGroups = lo.Filter(activeNodeGroups, func(ng scalehist.NodeGroupInfo, _ int) bool {
		return ng.CreationTimestamp.Before(scenarioEndTime.Add(d.scenarioTolerationInterval)) && ng.CreationTimestamp.After(prevScenarioEndTime.Add(d.scenarioTolerationInterval)) && ng.CurrentSize <= ng.TargetSize // required as CA can scaled down a nodegroup in between due to underutilization and we need to filter such rows.
		//return ng.CreationTimestamp.Before(scenarioEndTime) && ng.CreationTimestamp.After(prevScenarioEndTime)
	})
	nodeGroups = CollapseNodeGroups(nodeGroups)
	nodeGroupsByName := lo.SliceToMap(nodeGroups, func(item scalehist.NodeGroupInfo) (string, scalehist.NodeGroupInfo) {
		return item.Name, item
	})
	for _, ng := range activeNodeGroups {
		_, ok := nodeGroupsByName[ng.Name]
		if !ok {
			//if ng.CurrentSize != ng.TargetSize {
			//	ng.CurrentSize = ng.TargetSize
			//}
			//TODO remove this
			ng.CurrentSize = ng.TargetSize //this is required to not treat this nodegroup as a scaledup nodegroup due to currentSize != targetSize.
			nodeGroups = append(nodeGroups, ng)
			nodeGroupsByName[ng.Name] = ng
		}
	}
	//for i, s := range scenarios {
	//	scenarios[i].NodeGroups = getLatestNodeGroupsByName(s.StartTime.Add(d.scenarioCoalesceInterval), activeNodeGroups)
	//}
	return
}

// TODO address the bug for currentSize != targetSize
func (d *defaultAnalyzer) PopulateNodeGroups(scenarios []scalehist.Scenario) error {
	activeNodeGroups, err := d.dataAccess.LoadAllActiveNodeGroupsDesc()
	if err != nil {
		return err
	}
	for i, s := range scenarios {
		scenarios[i].NodeGroups = getLatestNodeGroupsByName(s.StartTime.Add(d.scenarioCoalesceInterval), activeNodeGroups)
	}
	return nil
}

func (d *defaultAnalyzer) PopulateCASettingsInfo(scenarios []scalehist.Scenario) error {
	for i, s := range scenarios {
		//TODO enhance to load all CA rows at once
		eventCAAssoc, err := d.dataAccess.GetEventCAAssocWithEventUID(s.ScaleUpEvents[0].UID)
		if err != nil || eventCAAssoc == nil {
			return err
		}
		caSettings, err := d.dataAccess.GetCADeploymentWithHash(eventCAAssoc.CASettingsHash)
		if err != nil || caSettings == nil {
			return err
		}
		scenarios[i].CASettings = *caSettings
	}
	return nil
}

func (d *defaultAnalyzer) PopulateNodes(scenarios []scalehist.Scenario) error {
	for i, s := range scenarios {
		nodes, err := d.dataAccess.GetLatestNodesBeforeAndNotDeleted(s.StartTime)
		if err != nil {
			return err
		}
		scenarios[i].Nodes = nodes
	}
	return nil
}

func (d *defaultAnalyzer) Analyze(ctx context.Context) (analysis scalehist.Analysis, err error) {
	events, err := d.dataAccess.LoadAllEvents()
	if err != nil {
		return
	}
	candidateScenarios, err := d.ComputeCandidateScenarios(events)
	if err != nil {
		return
	}
	scenarios := CoalesceScenarios(candidateScenarios, d.scenarioCoalesceInterval)
	//err = d.PopulateNodeGroups(scenarios)
	//if err != nil {
	//	return
	//}
	//err = d.PopulateScaledUpNodeGroups(scenarios)
	//if err != nil {
	//	return
	//}
	err = d.PopulateCASettingsInfo(scenarios)
	if err != nil {
		return
	}
	err = d.PopulateNodes(scenarios)
	if err != nil {
		return
	}
	//find first TSU event, get time
	// P = get all Pods from pod_info where node name is not set and createdTime <= TSU Time
	// R = GET ALL POD NAMES FROM EVENT TABLE WHERE PodScheduled IS TRUE NA DCREATED TIME <= TSU TIME
	// compute (p-R) - this is the unscheduledPods
	//	d.dataAccess.GetLatestUnscheduledPodsBeforeTimestamp() //TODO: implement me
	analysis = scalehist.Analysis{
		Name:               d.name,
		CoalesceInterval:   d.scenarioCoalesceInterval.String(),
		TolerationInterval: d.scenarioTolerationInterval.String(),
		Scenarios:          scenarios,
	}
	return
}

func getTriggeredScaleUpEvents(events []scalehist.EventInfo) []scalehist.EventInfo {
	return lo.Filter(events, func(item scalehist.EventInfo, index int) bool {
		if item.Reason == "TriggeredScaleUp" {
			return true
		}
		return false
	})
}

func getScheduledPodUIDsInEventsBefore(events []scalehist.EventInfo, timestamp time.Time) (podNames []string) {
	podNames = lo.FilterMap(events, func(item scalehist.EventInfo, _ int) (string, bool) {
		if item.Reason == "Scheduled" && item.EventTime.Before(timestamp) {
			return item.InvolvedObjectUID, true
		}
		return "", false
	})
	return
}

var _ scalehist.Analyzer = (*defaultAnalyzer)(nil)

func getBaseName(filepath string) string {
	name := path.Base(filepath)
	ext := path.Ext(filepath)
	name = strings.TrimSuffix(name, ext)
	return name
}

func NewDefaultAnalyzer(dataDBPath string, scenarioCoalesceInterval, scenarioTolerationInterval time.Duration) (scalehist.Analyzer, error) {
	dataAccess := db.NewDataAccess(dataDBPath)
	err := dataAccess.Init()
	if err != nil {
		return nil, err
	}

	analyzer := &defaultAnalyzer{
		name:                       getBaseName(dataDBPath),
		dataAccess:                 dataAccess,
		scenarioCoalesceInterval:   scenarioCoalesceInterval,
		scenarioTolerationInterval: scenarioTolerationInterval,
	}
	return analyzer, nil
}

func computeMaxSystemAndCriticalComponentsRequests(podInfos []scalehist.PodInfo) (systemComponentRequests corev1.ResourceList,
	criticalComponentRequests corev1.ResourceList, err error) {
	podInfos = lo.Filter(podInfos, HasNodeName) // only scheduled pods
	podInfosByNodeName := lo.GroupBy(podInfos, GetNodeName)
	var sysRequests []corev1.ResourceList
	var critRequests []corev1.ResourceList
	for _, podInfosOnNode := range podInfosByNodeName {
		sysRequest := sumSystemComponentResources(podInfosOnNode)
		critRequest := sumCriticalComponentResources(podInfosOnNode)
		if err != nil {
			return nil, nil, err
		}
		sysRequests = append(sysRequests, sysRequest)
		critRequests = append(critRequests, critRequest)
	}
	systemComponentRequests = ComputeMaximalResource(sysRequests...)
	criticalComponentRequests = ComputeMaximalResource(critRequests...)
	return
}

func ComputeMaximalResource(resources ...corev1.ResourceList) corev1.ResourceList {
	maxResource := make(corev1.ResourceList)
	for _, r := range resources {
		for name, quant := range r {
			val, ok := maxResource[name]
			if !ok {
				maxResource[name] = quant
				continue
			}
			if val.Cmp(quant) < 0 {
				maxResource[name] = quant
			}
		}
	}
	//memRes, ok := maxResource[corev1.ResourceMemory]
	//if ok {
	//	slog.Info("memRes Format", "format", memRes.Format)
	//	absVal, ok := memRes.AsInt64()
	//	if ok {
	//		memRes, err := resource.ParseQuantity(fmt.Sprintf("%dKi", absVal/1024))
	//		if err != nil {
	//			maxResource[corev1.ResourceMemory] = memRes
	//		}
	//	}
	//	//resource.ParseQuantity(
	//}
	return maxResource
}

func sumSystemComponentResources(podInfos []scalehist.PodInfo) corev1.ResourceList {
	sysPods := lo.Filter(podInfos, IsSystemComponent)
	sysPodsRequests := lo.Map(sysPods, func(item scalehist.PodInfo, _ int) corev1.ResourceList {
		return item.Requests
	})
	return scalehist.SumResources(sysPodsRequests)
}

func sumCriticalComponentResources(podInfos []scalehist.PodInfo) corev1.ResourceList {
	sysPods := lo.Filter(podInfos, IsCriticalComponent)
	sysPodsRequests := lo.Map(sysPods, func(item scalehist.PodInfo, _ int) corev1.ResourceList {
		return item.Requests
	})
	return scalehist.SumResources(sysPodsRequests)
}

func cmpPodInfoByCreationTimestamp(a, b scalehist.PodInfo) int {
	return a.CreationTimestamp.Compare(b.CreationTimestamp)
}

func cmpEventInfoByEventTimestamp(a, b scalehist.EventInfo) int {
	return a.EventTime.Compare(b.EventTime)
}

func HasNodeName(podInfo scalehist.PodInfo, _ int) bool {
	return podInfo.NodeName != ""
}

func GetNodeName(podInfo scalehist.PodInfo) string {
	return podInfo.NodeName
}

func IsSystemComponent(podInfo scalehist.PodInfo, _ int) bool {
	// system component label: "gardener.cloud/role": "system-component",
	val, ok := podInfo.Labels["gardener.cloud/role"]
	return ok && val == "system-component"
}

func IsCriticalComponent(podInfo scalehist.PodInfo, _ int) bool {
	// critical component: "node.gardener.cloud/critical-component": "true",
	val, ok := podInfo.Labels["node.gardener.cloud/critical-component"]
	return ok && val == "true"
}
