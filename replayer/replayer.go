package replayer

import (
	"context"
	"fmt"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/db"
	gst "github.com/elankath/gardener-scaling-types"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	"log/slog"
	"os"
	"strings"
	"time"
)

const DefaultStabilizeInterval = time.Duration(45 * time.Second)
const DefaultTotalReplayTime = time.Duration(1 * time.Hour)
const DefaultReplayInterval = time.Duration(5 * time.Minute)

type defaultReplayer struct {
	dataAccess          *db.DataAccess
	clientSet           *kubernetes.Clientset
	params              gsh.ReplayerParams
	initNodes           []gst.NodeInfo
	lastScenarios       []gsh.Scenario
	lastClusterSnapshot gsh.ClusterSnapshot
	lastReplayTime      time.Time
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
	pods, err := d.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the pods", "error", err)
		return err
	}
	for _, pod := range pods.Items {
		err = d.clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			slog.Error("cannot delete the pod", "pod.Name", pod.Name, "error", err)
			return err
		}
	}
	nodes, err := d.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the nodes", "error", err)
		return err
	}
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
		err = d.clientSet.SchedulingV1().PriorityClasses().Delete(ctx, pc.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Error("cannot delete the priority class", "pc.Name", pc.Name, "error", err)
			return err
		}
	}
	slog.Info("cleaned the cluster, deleted all pods in all namespaces")
	return nil
}

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

func computeDeltaWork(lastClusterSnapshot, currentClusterSnapshot gsh.ClusterSnapshot) (dW deltaWork) {
	lastPods := lastClusterSnapshot.Pods
	currentPods := currentClusterSnapshot.Pods

	lastUIDs := lastClusterSnapshot.GetPodUIDs()
	currUIDs := currentClusterSnapshot.GetPodUIDs()

	podsToDeleteUIDs := lastUIDs.Difference(currUIDs)
	podsToDeployUIDs := currUIDs.Difference(lastUIDs)

	dW.podsToDelete = lo.Filter(lastPods, func(item gst.PodInfo, index int) bool {
		return podsToDeleteUIDs.Has(item.UID)
	})

	dW.podsToDeploy = lo.Filter(currentPods, func(item gst.PodInfo, index int) bool {
		return podsToDeployUIDs.Has(item.UID)
	})

	lastPCs := lastClusterSnapshot.PriorityClasses
	lastPCUIDs := lastClusterSnapshot.GetPriorityClassUIDs()
	currPCs := currentClusterSnapshot.PriorityClasses
	currPCUIDs := currentClusterSnapshot.GetPriorityClassUIDs()

	pcsToDeleteUIDs := lastPCUIDs.Difference(currPCUIDs)
	pcsToDeployUIDs := currPCUIDs.Difference(lastPCUIDs)

	dW.pcsToDelete = lo.Filter(lastPCs, func(item gst.PriorityClassInfo, index int) bool {
		return pcsToDeleteUIDs.Has(string(item.UID))
	})

	dW.pcsToDeploy = lo.Filter(currPCs, func(item gst.PriorityClassInfo, index int) bool {
		return pcsToDeployUIDs.Has(string(item.UID))
	})

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
	pod.Spec.NodeName = ""
	pod.Status.NominatedNodeName = ""
	return pod
}

func (d *defaultReplayer) applyWork(ctx context.Context, work deltaWork) error {
	for _, pc := range work.pcsToDelete {
		pc := pc.PriorityClass
		err := d.clientSet.SchedulingV1().PriorityClasses().Delete(ctx, pc.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the priorityclass %q: %w", pc.Name, err)
		}
		slog.Info("successfully deleted priority class", "name", pc.Name)
	}

	work.pcsToDeploy = lo.Filter(work.pcsToDeploy, func(item gst.PriorityClassInfo, index int) bool {
		return item.Name != "system-cluster-critical" && item.Name != "system-node-critical"
	})

	for _, pc := range work.pcsToDeploy {
		pc := pc.PriorityClass
		_, err := d.clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pc, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("cannot create the priorityclass %q: %w", pc.Name, err)
		}
		slog.Info("successfully created priority class", "name", pc.Name)
	}

	for _, pod := range work.podsToDelete {
		err := d.clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the pod %q: %w", pod.Name, err)
		}
		slog.Info("successfully deleted pod", "name", pod.Name)
	}
	for _, pod := range work.podsToDeploy {
		corePod := getCorePodFromPodInfo(pod)
		pd, err := d.clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &corePod, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("cannot create the pod %q: %w", pd.Name, err)
		}
		slog.Info("successfully created pod", "name", pod.Name)
	}
	slog.Info("applied deltaWork", "deltaWork", work)
	return nil
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

func (d *defaultReplayer) doReplay(ctx context.Context) error {
	replayTime, err := d.getReplayTime()
	if err != nil {
		return err
	}
	defer func() { d.lastReplayTime = replayTime }()
	slog.Info("getting cluster snapshot at time", "snapshotTime", replayTime)
	clusterSnapshot, err := d.GetRecordedClusterSnapshot(replayTime)
	if err != nil {
		return err
	}
	// |lCs|---------------|Cs| delta -> Cs - lCs
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
	deltaWk := computeDeltaWork(d.lastClusterSnapshot, clusterSnapshot)
	if deltaWk.IsEmpty() {
		slog.Info("no delta work to apply.")
		return nil
	}
	err = d.applyWork(ctx, deltaWk)
	if err != nil {
		return err
	}
	slog.Info("applied work, waiting for cluster to stabilize", "stabilizeInterval", d.params.StabilizeInterval)
	<-time.After(d.params.StabilizeInterval)
	d.appendScenario(d.lastClusterSnapshot, clusterSnapshot)
	d.lastClusterSnapshot = clusterSnapshot
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

func (d *defaultReplayer) appendScenario(last, curr gsh.ClusterSnapshot) {

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
