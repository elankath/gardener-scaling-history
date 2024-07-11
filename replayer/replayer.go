package replayer

import (
	"context"
	"fmt"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/recorder"
	gst "github.com/elankath/gardener-scaling-types"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

const DefaultStabilizeInterval = time.Duration(30 * time.Second)
const DefaultTotalReplayTime = time.Duration(1 * time.Hour)
const DefaultReplayInterval = time.Duration(5 * time.Minute)

type defaultReplayer struct {
	dataAccess          *db.DataAccess
	clientSet           *kubernetes.Clientset
	params              gsh.ReplayerParams
	replayLoop          int
	initNodes           []gst.NodeInfo
	lastScenarios       []gsh.Scenario
	lastClusterSnapshot gsh.ClusterSnapshot
	lastReplayTime      time.Time
	report              *gsh.ReplayReport
	reportPath          string
}

var _ gsh.Replayer = (*defaultReplayer)(nil)

func NewDefaultReplayer(params gsh.ReplayerParams) (gsh.Replayer, error) {
	// Load kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", params.VirtualClusterKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}
	return &defaultReplayer{
		dataAccess: db.NewDataAccess(params.DBPath),
		clientSet:  clientset,
		params:     params,
	}, nil
}

func WriteAutoScalerConfig(autoscalerConfig gst.AutoScalerConfig, path string) error {
	bytes, err := json.Marshal(autoscalerConfig)
	if err != nil {
		return err
	}
	err = os.WriteFile(path, bytes, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (d *defaultReplayer) CleanCluster(ctx context.Context) error {
	slog.Info("Cleaning the virtual cluster  nodes, priority-classes...")
	err := clearPods(ctx, d.clientSet)
	if err != nil {
		return err
	}
	nodes, err := d.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the nodes", "error", err)
		return err
	}
	slog.Info("Deleting nodes.", "numNodes", len(nodes.Items))
	for _, node := range nodes.Items {
		err = d.clientSet.CoreV1().Nodes().Delete(ctx, node.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Error("cannot delete the node", "node.Name", node.Name, "error", err)
			return err
		}
	}

	pcs, err := d.clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the priority classes", "error", err)
		return err
	}
	for _, pc := range pcs.Items {
		if strings.HasPrefix(pc.Name, "system-") {
			continue
		}
		slog.Info("Deleting Priority Class", "priorityCLass", pc.Name)
		err = d.clientSet.SchedulingV1().PriorityClasses().Delete(ctx, pc.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Error("cannot delete the priority class", "pc.Name", pc.Name, "error", err)
			return err
		}
	}
	slog.Info("cleaned the cluster, deleted all pods in all namespaces")
	return nil
}

func clearPods(ctx context.Context, c *kubernetes.Clientset) error {
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the pods", "error", err)
		return err
	}
	for _, pod := range pods.Items {
		err = c.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			slog.Error("cannot delete the pod", "pod.Name", pod.Name, "error", err)
			return err
		}
	}
	return err
}

func FilenameWithoutExtension(fn string) string { return strings.TrimSuffix(fn, path.Ext(fn)) }

func (d *defaultReplayer) Start(ctx context.Context) error {
	err := d.CleanCluster(ctx)
	if err != nil {
		return err
	}
	err = d.dataAccess.Init()
	if err != nil {
		return err
	}

	replayTime, err := d.getReplayTime()
	if err != nil {
		return err
	}

	d.initNodes, err = d.dataAccess.LoadNodeInfosBefore(replayTime)
	if err != nil {
		return fmt.Errorf("cannot get the initial node infos: %w", err)
	}
	if len(d.initNodes) == 0 {
		return fmt.Errorf("no initial nodeinfos available before replay time %q", replayTime)
	}
	d.reportPath = path.Join(d.params.ReportDir, "report.json")
	//clusterSnapshot, err := d.GetInitialClusterSnapshot()
	//if err != nil {
	//	return err
	//}
	//err = WriteAutoScalerConfig(clusterSnapshot.AutoscalerConfig, d.params.VirtualAutoScalerConfigPath)
	//if err != nil {
	//	return fmt.Errorf("cannot write autoscaler config at time %q to path %q: %w", clusterSnapshot.SnapshotTime, d.params.VirtualAutoScalerConfigPath, err)
	//}
	//d.lastClusterSnapshot = clusterSnapshot
	//d.lastReplayTime = clusterSnapshot.SnapshotTime
	//slog.Info("wrote initial autoscaler config, waiting for stabilization", "config", d.params.VirtualAutoScalerConfigPath,
	//	"stabilizeInterval", d.params.StabilizeInterval)
	//<-time.After(d.params.StabilizeInterval)
	return nil
}

type deltaWork struct {
	podsToDeploy []gst.PodInfo
	podsToDelete []gst.PodInfo
	pcsToDelete  []gst.PriorityClassInfo
	pcsToDeploy  []gst.PriorityClassInfo
}

func (d deltaWork) IsEmpty() bool {
	return len(d.podsToDelete) == 0 && len(d.podsToDeploy) == 0
}

func (d deltaWork) String() string {
	var sb strings.Builder
	sb.WriteString("podsToDelete: (")
	lo.Reduce(d.podsToDelete, func(agg *strings.Builder, item gst.PodInfo, index int) *strings.Builder {
		agg.WriteString(item.Name + ",")
		return agg
	}, &sb)
	sb.WriteString(")")
	sb.WriteString("podsToDeploy: (")
	lo.Reduce(d.podsToDeploy, func(agg *strings.Builder, item gst.PodInfo, index int) *strings.Builder {
		agg.WriteString(item.Name + ",")
		return agg
	}, &sb)
	sb.WriteString(")")
	return sb.String()
}

func GetPodsByUID(pods []gst.PodInfo) (podsMap map[string]gst.PodInfo) {
	return lo.KeyBy(pods, func(item gst.PodInfo) string {
		return item.UID
	})
}

func computeWork(currentClusterSnapshot gsh.ClusterSnapshot) (dW deltaWork) {
	//lastPods := lastClusterSnapshot.Pods
	//currentPods := currentClusterSnapshot.Pods
	//
	//lastUIDs := lastClusterSnapshot.GetPodUIDs()
	//currUIDs := currentClusterSnapshot.GetPodUIDs()
	//
	//podsToDeleteUIDs := lastUIDs.Difference(currUIDs)
	//podsToDeployUIDs := currUIDs.Difference(lastUIDs)
	//
	//dW.podsToDelete = lo.Filter(lastPods, func(item gst.PodInfo, index int) bool {
	//	return podsToDeleteUIDs.Has(item.UID)
	//})
	//
	//dW.podsToDeploy = lo.Filter(currentPods, func(item gst.PodInfo, index int) bool {
	//	return podsToDeployUIDs.Has(item.UID)
	//})
	//
	//lastPCs := lastClusterSnapshot.PriorityClasses
	//lastPCUIDs := lastClusterSnapshot.GetPriorityClassUIDs()
	//currPCs := currentClusterSnapshot.PriorityClasses
	//currPCUIDs := currentClusterSnapshot.GetPriorityClassUIDs()
	//
	//pcsToDeleteUIDs := lastPCUIDs.Difference(currPCUIDs)
	//pcsToDeployUIDs := currPCUIDs.Difference(lastPCUIDs)
	//
	//dW.pcsToDelete = lo.Filter(lastPCs, func(item gst.PriorityClassInfo, index int) bool {
	//	return pcsToDeleteUIDs.Has(string(item.UID))
	//})
	//
	//dW.pcsToDeploy = lo.Filter(currPCs, func(item gst.PriorityClassInfo, index int) bool {
	//	return pcsToDeployUIDs.Has(string(item.UID))
	//})

	return
}

func getCorePodFromPodInfo(podInfo gst.PodInfo) corev1.Pod {
	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels:    podInfo.Labels,
			Name:      podInfo.Name,
			Namespace: podInfo.Namespace,
			UID:       types.UID(podInfo.UID),
		},
		Spec: podInfo.Spec,
	}
	//pod.Spec.NodeName = ""
	pod.Status.NominatedNodeName = podInfo.NominatedNodeName
	return pod
}

func getNamespaces(ctx context.Context, clientSet *kubernetes.Clientset) (sets.Set[string], error) {
	virtualNamespaceList, err := clientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the namespaces in virtual cluster")
		return nil, err
	}
	return sets.New[string](lo.Map(virtualNamespaceList.Items, func(item corev1.Namespace, index int) string {
		return item.Name
	})...), nil
}

func deployNamespaces(ctx context.Context, clientSet *kubernetes.Clientset, nss ...string) error {
	for _, ns := range nss {
		namespace := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := clientSet.CoreV1().Namespaces().Create(ctx, &namespace, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("cannot create the namespace %q in virtual cluster: %w", ns, err)
		}
	}
	return nil
}

func deleteNamespaces(ctx context.Context, clientSet *kubernetes.Clientset, nss ...string) error {
	for _, ns := range nss {
		err := clientSet.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the namespace %q in virtual cluster: %w", ns, err)
		}
	}
	return nil
}

func (d *defaultReplayer) applyWork(ctx context.Context, clusterSnapshot gsh.ClusterSnapshot) (workDone bool, err error) {

	virtualPCs, err := d.clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the priority classes in virtual cluster: %w", err)
		return
	}
	virtualPCsMap := lo.KeyBy(virtualPCs.Items, func(item schedulingv1.PriorityClass) string {
		return item.Name
	})
	for _, pcInfo := range clusterSnapshot.PriorityClasses {
		_, ok := virtualPCsMap[pcInfo.Name]
		if ok {
			continue
		}
		_, err = d.clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pcInfo.PriorityClass, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("cannot create the priority class %s: %w", pcInfo.Name, err)
		}
		slog.Info("successfully create the priority class", "pc.Name", pcInfo.Name)
		workDone = true
	}

	podNamespaces := clusterSnapshot.GetPodNamspaces()
	virtualNamespaces, err := getNamespaces(ctx, d.clientSet)
	if err != nil {
		return
	}

	//nsToDelete := virtualNamespaces.Difference(podNamespaces)
	nsToDeploy := podNamespaces.Difference(virtualNamespaces)

	err = deployNamespaces(ctx, d.clientSet, nsToDeploy.UnsortedList()...)
	if err != nil {
		return
	}

	virtualPodsList, err := d.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	virtualPodsByName := lo.Associate(virtualPodsList.Items, func(item corev1.Pod) (string, gst.PodInfo) {
		return item.Name, recorder.PodInfoFromPod(&item)
	})
	clusterSnapshotPodsByName := lo.KeyBy(clusterSnapshot.Pods, func(item gst.PodInfo) string {
		return item.Name
	})
	for _, podInfo := range virtualPodsByName {
		snapshotPodInfo, ok := clusterSnapshotPodsByName[podInfo.Name]
		if ok && (snapshotPodInfo.NominatedNodeName == podInfo.NominatedNodeName || snapshotPodInfo.NodeName == podInfo.NodeName) {
			continue
		}
		err = d.clientSet.CoreV1().Pods(podInfo.Namespace).Delete(ctx, podInfo.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			err = fmt.Errorf("cannot delete the pod %q: %w", podInfo.Name, err)
			return
		}
		delete(virtualPodsByName, podInfo.UID)
		workDone = true
	}
	deployCount := 0
	for _, podInfo := range clusterSnapshot.Pods {
		_, ok := virtualPodsByName[podInfo.Name]
		if ok {
			continue
		}
		pod := getCorePodFromPodInfo(podInfo)
		deployCount++
		slog.Info("deploying pod", "deployCount", deployCount, "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "pod.NodeName", pod.Spec.NodeName)
		_, err = d.clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		if err != nil {
			err = fmt.Errorf("cannot create the pod  %s: %w", pod.Name, err)
			return
		}
		workDone = true
	}
	if deployCount > 0 {
		slog.Info("applied work, waiting for cluster to stabilize", "stabilizeInterval", d.params.StabilizeInterval)
	} else {
		slog.Info("No work to apply, waiting for stabilizeInterval before trying again", "stabilizeInterval", d.params.StabilizeInterval)
	}
	return
}

func (d *defaultReplayer) getReplayTime() (replayTime time.Time, err error) {
	if d.lastReplayTime.IsZero() {
		replayTime, err = d.dataAccess.GetInitialRecorderStartTime()
		if err != nil {
			return
		}
		replayTime = replayTime.Add(d.params.StabilizeInterval).UTC()
		return
	}
	replayTime = d.lastReplayTime.Add(d.params.ReplayInterval).UTC()
	now := time.Now().UTC()
	if replayTime.After(now) {
		replayTime = now
	}
	return
}

func BuildReadyConditions() []corev1.NodeCondition {
	lastTransition := time.Now().Add(-time.Minute)
	return []corev1.NodeCondition{
		{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeNetworkUnavailable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeDiskPressure,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeMemoryPressure,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		/* This has been recently renamed. For compatibility with 1.6 lets don't populate it at all.
		{
			Type:               apiv1.NodeInodePressure,
			Status:             apiv1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		*/
	}
}

func adjustNode(clientSet *kubernetes.Clientset, nodeName string, nodeStatus corev1.NodeStatus) error {

	nd, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot get node with name %q: %w", nd.Name, err)
	}
	nd.Spec.Taints = lo.Filter(nd.Spec.Taints, func(item corev1.Taint, index int) bool {
		return item.Key != "node.kubernetes.io/not-ready" && item.Key != "node.gardener.cloud/critical-components-not-ready" && item.Key != "node.cloudprovider.kubernetes.io/uninitialized"
	})
	nd, err = clientSet.CoreV1().Nodes().Update(context.Background(), nd, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("cannot update node with name %q: %w", nd.Name, err)
	}
	//nd.Status.Conditions = cloudprovider.BuildReadyConditions()
	//nd.Status.Phase = corev1.NodeRunning
	//TODO set the nodeInfo in node status
	nd.Status = nodeStatus
	nd.Status.Phase = corev1.NodeRunning
	nd, err = clientSet.CoreV1().Nodes().UpdateStatus(context.Background(), nd, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("cannot update the status of node with name %q: %w", nd.Name, err)
	}
	return nil
}

func synchronizeNodes(ctx context.Context, clientSet *kubernetes.Clientset, nodeInfos []gst.NodeInfo) (clusterNodes []corev1.Node, err error) {
	virtualNodeList, err := clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}
	virtualNodesMap := lo.KeyBy(virtualNodeList.Items, func(item corev1.Node) string {
		return item.Name
	})
	nodeInfosByName := lo.Associate(nodeInfos, func(item gst.NodeInfo) (string, struct{}) {
		return item.Name, struct{}{}
	})
	for _, vnode := range virtualNodeList.Items {
		_, ok := nodeInfosByName[vnode.Name]
		if ok {
			continue
		}
		err = clientSet.CoreV1().Nodes().Delete(ctx, vnode.Name, metav1.DeleteOptions{})
		if err != nil {
			err = fmt.Errorf("cannot delete the virtual node %q: %w", vnode.Name, err)
			return
		}
		delete(virtualNodesMap, vnode.Name)
	}
	for _, nodeInfo := range nodeInfos {
		oldVNode, exists := virtualNodesMap[nodeInfo.Name]
		var sameLabels, sameTaints bool
		if exists {
			sameLabels = maps.Equal(oldVNode.Labels, nodeInfo.Labels)
			sameTaints = slices.EqualFunc(oldVNode.Spec.Taints, nodeInfo.Taints, gst.IsEqualTaint)
		}
		if exists && sameLabels && sameTaints {
			continue
		}
		node := corev1.Node{
			ObjectMeta: metav1.ObjectMeta{

				Name:      nodeInfo.Name,
				Namespace: nodeInfo.Namespace,
				Labels:    nodeInfo.Labels,
			},
			Spec: corev1.NodeSpec{
				Taints:     nodeInfo.Taints,
				ProviderID: nodeInfo.ProviderID,
			},
			Status: corev1.NodeStatus{
				Capacity:    nodeInfo.Capacity,
				Allocatable: nodeInfo.Allocatable,
			},
		}
		nodeStatus := node.Status
		if !exists {
			_, err = clientSet.CoreV1().Nodes().Create(context.Background(), &node, metav1.CreateOptions{})
			if errors.IsAlreadyExists(err) {
				slog.Warn("node already exists. updating node", "nodeName", node.Name)
				_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
			}
			if err == nil {
				slog.Info("created node.", "nodeName", node.Name)
			}
		} else {
			slog.Info("updating node", "nodeName", node.Name)
			_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
		}
		if err != nil {
			err = fmt.Errorf("cannot create/update node with name %q: %w", node.Name, err)
			return
		}
		node.Status = nodeStatus
		// fixme : use buildCoreNodefromTemplate to construct node object
		node.Status.Conditions = BuildReadyConditions()
		err = adjustNode(clientSet, node.Name, node.Status)
		if err != nil {
			err = fmt.Errorf("cannot adjust the node with name %q: %w", node.Name, err)
			return
		}
	}
	virtualNodeList, err = clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}
	clusterNodes = virtualNodeList.Items
	return
}

func (d *defaultReplayer) doReplay(ctx context.Context) error {
	d.replayLoop++
	replayTime, err := d.getReplayTime()
	if err != nil {
		return err
	}
	defer func() { d.lastReplayTime = replayTime }()
	slog.Info("getting cluster snapshot at time", "replayLoop", d.replayLoop, "snapshotTime", replayTime)
	clusterSnapshot, err := d.GetRecordedClusterSnapshot(replayTime)
	if err != nil {
		return err
	}
	if clusterSnapshot.AutoscalerConfig.Hash != d.lastClusterSnapshot.AutoscalerConfig.Hash {
		slog.Info("wrote autoscaler config", "prevHash", d.lastClusterSnapshot.AutoscalerConfig.Hash, "currHash", clusterSnapshot.AutoscalerConfig.Hash)
		err = WriteAutoScalerConfig(clusterSnapshot.AutoscalerConfig, d.params.VirtualAutoScalerConfigPath)
		if err != nil {
			return fmt.Errorf("cannot write autoscaler config at time %q to path %q: %w", clusterSnapshot.SnapshotTime, d.params.VirtualAutoScalerConfigPath, err)
		}
		slog.Info("waiting for stabilization", "config", d.params.VirtualAutoScalerConfigPath,
			"stabilizeInterval", d.params.StabilizeInterval)
		<-time.After(d.params.StabilizeInterval)
	}
	virtualNodes, err := synchronizeNodes(ctx, d.clientSet, clusterSnapshot.Nodes)
	if err != nil {
		return fmt.Errorf("cannot synchronize the nodes from actual cluster: %w", err)
	}
	virtualNodeNames := lo.Map(virtualNodes, func(item corev1.Node, index int) string {
		return item.Name
	})
	err = deletePodsNotBelongingTo(ctx, d.clientSet, virtualNodeNames)
	if err != nil {
		return err
	}
	workDone, err := d.applyWork(ctx, clusterSnapshot)
	if err != nil {
		return err
	}
	if !workDone {
		slog.Info("no work done", "replayLoop", d.replayLoop)
	}
	<-time.After(d.params.StabilizeInterval)
	err = d.appendScenario(ctx, replayTime, clusterSnapshot)
	if err != nil {
		return err
	}
	d.lastClusterSnapshot = clusterSnapshot
	return nil
}

func deletePodsNotBelongingTo(ctx context.Context, clientSet *kubernetes.Clientset, nodes []string) error {
	nodeNames := sets.New[string](nodes...)
	podsList, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
	}
	for _, pod := range podsList.Items {
		if nodeNames.Has(pod.Spec.NodeName) {
			continue
		}
		err = clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the pod %q in namespace %q: %w", pod.Name, pod.Namespace, err)
		}
	}
	return nil
}

func (d *defaultReplayer) Replay(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			slog.Info("context has expired, exiting replayer")
			return nil
		default:
			err := d.doReplay(ctx)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *defaultReplayer) Close() error {
	return d.dataAccess.Close()
}

func (d *defaultReplayer) GetRecordedClusterSnapshot(startTime time.Time) (cs gsh.ClusterSnapshot, err error) {

	mccs, err := d.dataAccess.LoadMachineClassInfosBefore(startTime)
	if err != nil {
		return
	}
	mcds, err := d.dataAccess.LoadMachineDeploymentInfosBefore(startTime)
	if err != nil {
		return
	}
	workerPools, err := d.dataAccess.LoadWorkerPoolInfosBefore(startTime)
	if err != nil {
		return
	}
	var autoscalerConfig gst.AutoScalerConfig
	autoscalerConfig.NodeTemplates, err = GetNodeTemplates(mccs, mcds)
	if err != nil {
		return
	}
	cs.PriorityClasses, err = d.dataAccess.LoadLatestPriorityClassInfoBeforeSnapshotTime(startTime)
	if err != nil {
		return
	}
	autoscalerConfig.NodeGroups, err = GetNodeGroups(mcds, workerPools)
	if err != nil {
		return
	}

	autoscalerConfig.CASettings, err = d.dataAccess.LoadCASettingsBefore(startTime)
	cs.AutoscalerConfig = autoscalerConfig
	cs.AutoscalerConfig.Mode = gst.AutoscalerReplayerMode
	cs.SnapshotTime = startTime
	cs.AutoscalerConfig.InitNodes = d.initNodes
	cs.AutoscalerConfig.Hash = cs.AutoscalerConfig.GetHash()
	cs.Pods, err = d.dataAccess.GetLatestPodInfosBeforeSnapshotTime(startTime)
	if err != nil {
		return
	}

	cs.Nodes, err = d.dataAccess.LoadNodeInfosBefore(startTime)
	if err != nil {
		return
	}

	return
}

func (d *defaultReplayer) GetParams() gsh.ReplayerParams {
	//TODO implement me
	panic("implement me")
}

func getNodeGroupsByPoolZone(nodeGroupsByName map[string]gst.NodeGroupInfo) map[gsh.PoolZone]gst.NodeGroupInfo {
	poolZoneMap := make(map[gsh.PoolZone]gst.NodeGroupInfo)
	for _, ng := range nodeGroupsByName {
		poolKey := gsh.PoolZone{
			PoolName: ng.PoolName,
			Zone:     ng.Zone,
		}
		poolZoneMap[poolKey] = ng
	}
	return poolZoneMap
}

func (d *defaultReplayer) appendScenario(ctx context.Context, replayTime time.Time, clusterSnapshot gsh.ClusterSnapshot) error {
	postVirtualNodesList, err := d.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
	}

	var scaledUpNodes []gst.NodeInfo
	postVirtualNodes := postVirtualNodesList.Items
	preVirtualNodesMap := lo.KeyBy(clusterSnapshot.Nodes, func(item gst.NodeInfo) string {
		return item.Name
	})
	for _, node := range postVirtualNodes {
		_, ok := preVirtualNodesMap[node.Name]
		if ok {
			continue
		}
		//TODO how to replay csi nodes ?
		scaledUpNodes = append(scaledUpNodes, gsh.NodeInfoFromNode(&node, 0))
	}
	if len(scaledUpNodes) == 0 {
		slog.Warn("no scaled up nodes present, so skipping this scenario", "replayTime", replayTime)
		return nil
	}
	var s gsh.Scenario
	s.UnscheduledPods = clusterSnapshot.GetPodsWithScheduleStatus(gst.PodUnscheduled)
	s.NominatedPods = clusterSnapshot.GetPodsWithScheduleStatus(gst.PodScheduleNominated)
	s.ScheduledPods = clusterSnapshot.GetPodsWithScheduleStatus(gst.PodScheduleCommited)
	s.ExistingNodes = clusterSnapshot.Nodes
	s.ScaledUpNodes = scaledUpNodes
	s.ScaledUpNodeGroups = make(map[string]int)
	poolZoneMap := getNodeGroupsByPoolZone(clusterSnapshot.AutoscalerConfig.NodeGroups)
	for _, node := range scaledUpNodes {
		poolZone := gsh.PoolZone{
			PoolName: node.Labels["worker.gardener.cloud/pool"],
			Zone:     node.Labels["topology.kubernetes.io/zone"],
		}
		ng, ok := poolZoneMap[poolZone]
		if !ok {
			return fmt.Errorf("cannot find associated PoolZone %q for node %q", poolZone, node.Name)
		}
		s.ScaledUpNodeGroups[ng.Name]++
	}
	pods, err := d.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	for _, pod := range pods.Items {
		podInfo := recorder.PodInfoFromPod(&pod)
		if podInfo.PodScheduleStatus == gst.PodUnscheduled || podInfo.PodScheduleStatus == gst.PodSchedulePending {
			s.PendingUnscheduledPods = append(s.PendingUnscheduledPods, podInfo)
		}
	}
	if d.report == nil {
		d.report = &gsh.ReplayReport{
			StartTime: replayTime,
			Scenarios: make([]gsh.Scenario, 0),
		}
	}
	d.report.Scenarios = append(d.report.Scenarios, s)
	slog.Info("created scenario for", "replayTime", replayTime, "scaledupNodeGroups", s.ScaledUpNodeGroups)
	bytes, err := json.Marshal(d.report)
	if err != nil {
		return fmt.Errorf("cannot marshal the scenario report: %w", err)
	}

	err = os.WriteFile(d.reportPath, bytes, 0666)
	if err != nil {
		return fmt.Errorf("cannot write to report to file %q: %w", d.reportPath, err)
	}
	return nil

}

func GetNodeGroupNameFromMCCName(namespace, mccName string) string {
	idx := strings.LastIndex(mccName, "-")
	// mcc name - shoot--i585976--suyash-local-worker-1-z1-0af3f , we omit the hash from the mcc name to match it with the nodegroup name
	trimmedName := mccName[0:idx]
	return fmt.Sprintf("%s.%s", namespace, trimmedName)
}

func constructNodeTemplateFromMCC(mcc gsh.MachineClassInfo) gst.NodeTemplate {
	return gst.NodeTemplate{
		Name:         GetNodeGroupNameFromMCCName(mcc.Namespace, mcc.Name),
		Capacity:     mcc.Capacity,
		InstanceType: mcc.InstanceType,
		Region:       mcc.Region,
		Zone:         mcc.Zone,
		Labels:       mcc.Labels,
		Taints:       nil,
	}
}

func GetNodeTemplates(mccs []gsh.MachineClassInfo, mcds []gst.MachineDeploymentInfo) (nodeTemplates map[string]gst.NodeTemplate, err error) {
	nodeTemplates = make(map[string]gst.NodeTemplate)
	for _, mcc := range mccs {
		nodeTemplate := constructNodeTemplateFromMCC(mcc)
		nodeTemplates[nodeTemplate.Name] = nodeTemplate
	}
	for _, mcd := range mcds {
		ngName := fmt.Sprintf("%s.%s", mcd.Namespace, mcd.Name)
		nodeTemplate, ok := nodeTemplates[ngName]
		if !ok {
			err = fmt.Errorf("cannot find the node template for nodegroup: %s", ngName)
			return
		}
		nodeTemplate.Taints = mcd.Taints
		maps.Copy(nodeTemplate.Labels, mcd.Labels)
		nodeTemplate.Hash = nodeTemplate.GetHash()
		nodeTemplates[ngName] = nodeTemplate
	}
	return
}

func GetNodeGroups(mcds []gst.MachineDeploymentInfo, workerPools []gst.WorkerPoolInfo) (nodeGroups map[string]gst.NodeGroupInfo, err error) {
	nodeGroups = make(map[string]gst.NodeGroupInfo)
	workerPoolsByName := lo.KeyBy(workerPools, func(item gst.WorkerPoolInfo) string {
		return item.Name
	})
	for _, mcd := range mcds {
		workerPool, ok := workerPoolsByName[mcd.PoolName]
		if !ok {
			err = fmt.Errorf("cannot find pool name with name %q: %w", mcd.PoolName, gst.ErrKeyNotFound)
			return
		}
		nodeGroup := gst.NodeGroupInfo{
			Name:       fmt.Sprintf("%s.%s", mcd.Namespace, mcd.Name),
			PoolName:   mcd.PoolName,
			Zone:       mcd.Zone,
			TargetSize: mcd.Replicas,
			MinSize:    workerPool.Minimum,
			MaxSize:    workerPool.Maximum,
		}
		nodeGroup.Hash = nodeGroup.GetHash()
		nodeGroups[nodeGroup.Name] = nodeGroup
	}
	return
}

//func (d *defaultReplayer) GetInitialClusterSnapshot() (gsh.ClusterSnapshot, error) {
//	startTime, err := d.dataAccess.GetInitialRecorderStartTime()
//	if err != nil {
//		return gsh.ClusterSnapshot{}, err
//	}
//	startTime = startTime.Add(1 * time.Minute)
//
//	return d.GetRecordedClusterSnapshot(startTime)
//}
