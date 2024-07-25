package replayer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-common/clientutil"
	"github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/recorder"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"strconv"
	"strings"
	"time"
)

const DefaultStabilizeInterval = time.Duration(1 * time.Minute)
const DefaultReplayInterval = time.Duration(5 * time.Minute)

var ErrNoScenario = errors.New("no-scenario")

type defaultReplayer struct {
	dataAccess          *db.DataAccess
	clientSet           *kubernetes.Clientset
	params              gsh.ReplayerParams
	snapshotCount       int
	workCount           int
	lastScenarios       []gsh.Scenario
	lastClusterSnapshot gsc.ClusterSnapshot
	report              *gsh.ReplayReport
	reportPath          string
	scaleUpEventCounter int
	scaleUpEvents       []gsc.EventInfo
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

func WriteAutoscalerConfig(autoscalerConfig gsc.AutoscalerConfig, path string) error {
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

func (d *defaultReplayer) Replay(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			slog.Warn("context is cancelled/done, exiting re-player")
			return ctx.Err()
		default:
			replayMarkTime, err := d.getNextReplayMarkTime()
			if err != nil {
				return err
			}
			if replayMarkTime.IsZero() {
				slog.Info("no more scale ups to be replayed. Exiting")
				return nil
			}
			if replayMarkTime.After(time.Now()) {
				slog.Warn("replayMarkTime now exceeds current time. Exiting", "replayMarkTime", replayMarkTime)
				return nil
			}
			clusterSnapshot, err := d.GetRecordedClusterSnapshot(replayMarkTime)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					slog.Info("No more recorded work after replayMarkTime! Replay done", "replayMarkTime", replayMarkTime)
					return nil
				}
				return err
			}
			bytes, err := json.Marshal(clusterSnapshot)
			if err != nil {
				return err
			}
			snapShotPath := fmt.Sprintf("/tmp/clusterSnapshot-%d.json", clusterSnapshot.Number)
			_ = os.WriteFile(snapShotPath, bytes, 0644)

			slog.Info("obtained recorded cluster snapshot for replay.",
				"Number", clusterSnapshot.Number,
				"SnapshotTime", clusterSnapshot.SnapshotTime,
				"Hash", clusterSnapshot.Hash,
				"PrevHash", d.lastClusterSnapshot.Hash,
			)
			if clusterSnapshot.Hash == d.lastClusterSnapshot.Hash {
				slog.Info("skipping replay since clusterSnapshot.Hash unchanged from", "Hash", clusterSnapshot.Hash)
				d.lastClusterSnapshot = clusterSnapshot
				continue
			}
			var deletedPendingPods []corev1.Pod
			if clusterSnapshot.AutoscalerConfig.Hash != d.lastClusterSnapshot.AutoscalerConfig.Hash {
				slog.Info("writing autoscaler config", "snapshotNumber", clusterSnapshot.Number, "currHash", clusterSnapshot.AutoscalerConfig.Hash)
				// Before writing autoscaler config, I must delete any unscheduled Pods .
				// Other-wise CA will call VirtualNodeGroup.IncreaseSize just after AutoScalerConfig is written
				// which isn't good since we want VirtualNodeGroup.IncreaseSize to be called only AFTER computeAndApplyDeltaWork
				deletedPendingPods, err = deletePendingUnscheduledPods(ctx, d.clientSet)
				if err != nil {
					err = fmt.Errorf("cannot delete pendign unscheduled pods: %w", err)
					return err
				}
				err = WriteAutoscalerConfig(clusterSnapshot.AutoscalerConfig, d.params.VirtualAutoScalerConfigPath)
				if err != nil {
					err = fmt.Errorf("cannot write autoscaler config at time %q to path %q: %w", clusterSnapshot.SnapshotTime, d.params.VirtualAutoScalerConfigPath, err)
					return err
				}
				err = waitTillNodesStarted(ctx, d.clientSet, clusterSnapshot.Number, len(clusterSnapshot.AutoscalerConfig.ExistingNodes))
				if err != nil {
					return err
				}
			}
			_, err = d.computeAndApplyDeltaWork(ctx, clusterSnapshot, deletedPendingPods)
			if err != nil {
				return err
			}
			slog.Info("applied work, waiting for cluster to stabilize", "stabilizeInterval", d.params.StabilizeInterval, "workCount", d.workCount)
			select {
			case <-time.After(d.params.StabilizeInterval):
			case <-ctx.Done():
				slog.Warn("Context cancelled or timed out:", ctx.Err())
				return ctx.Err()
			}
			d.lastClusterSnapshot = clusterSnapshot
			scenario, err := d.createScenario(ctx, clusterSnapshot)
			if err != nil {
				if errors.Is(err, ErrNoScenario) {
					continue
				} else {
					return err
				}
			}
			err = d.appendScenario(scenario, clusterSnapshot)
			if err != nil {
				return err
			}
		}
	}
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

func (d *defaultReplayer) Start(ctx context.Context) error {
	err := d.CleanCluster(ctx)
	if err != nil {
		return err
	}
	err = d.dataAccess.Init()
	if err != nil {
		return err
	}

	//caSettings, err := d.dataAccess.LoadCASettingsBefore(time.Now().UTC())
	//if err != nil {
	//	return err
	//}
	//slog.Info("loaded casettings", "caSettings", caSettings)
	d.scaleUpEvents, err = d.dataAccess.LoadTriggeredScaleUpEvents()
	if err != nil {
		return err
	}
	if len(d.scaleUpEvents) == 0 {
		return fmt.Errorf("no TriggeredScaleUp events found in recorded data")
	}
	reportFileName := apputil.FilenameWithoutExtension(d.params.DBPath) + "-report.json"
	d.reportPath = path.Join(d.params.ReportDir, reportFileName)
	return nil
}

type PodWork struct {
	// ToDelete has the Pod names to delete
	ToDelete sets.Set[types.NamespacedName]
	ToDeploy []gsc.PodInfo
}

type PriorityClassWork struct {
	ToDeploy []gsc.PriorityClassInfo
	// TODO: consider introducing ToDelete sets.Set[string] here too
}
type NamespaceWork struct {
	ToCreate sets.Set[string]
	ToDelete sets.Set[string]
	// TODO: consider introducing ToDelete here too
}

type DeltaWork struct {
	Number            int
	PriorityClassWork PriorityClassWork
	NamespaceWork     NamespaceWork
	PodWork           PodWork
}

func (d DeltaWork) IsEmpty() bool {
	return len(d.PodWork.ToDeploy) == 0 &&
		len(d.PodWork.ToDelete) == 0 &&
		len(d.NamespaceWork.ToCreate) == 0 &&
		len(d.NamespaceWork.ToDelete) == 0 &&
		len(d.PriorityClassWork.ToDeploy) == 0
}

func (d DeltaWork) String() string {
	var sb strings.Builder
	sb.WriteString("#")
	sb.WriteString(strconv.Itoa(d.Number))
	sb.WriteString("| ")
	sb.WriteString(fmt.Sprintf("podsToDelete: (%s)", d.PodWork.ToDelete))
	sb.WriteString("podsToDeploy: (")
	lo.Reduce(d.PodWork.ToDeploy, func(agg *strings.Builder, item gsc.PodInfo, index int) *strings.Builder {
		agg.WriteString(item.Name + ",")
		return agg
	}, &sb)
	sb.WriteString(")")
	return sb.String()
}

func GetPodsByUID(pods []gsc.PodInfo) (podsMap map[string]gsc.PodInfo) {
	return lo.KeyBy(pods, func(item gsc.PodInfo) string {
		return item.UID
	})
}

func getCorePodFromPodInfo(podInfo gsc.PodInfo) corev1.Pod {
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
	pod.Spec.NodeName = podInfo.NodeName
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

func createNamespaces(ctx context.Context, clientSet *kubernetes.Clientset, nss ...string) error {
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
		slog.Info("created namespace", "namespace", ns)
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

func applyDeltaWork(ctx context.Context, clientSet *kubernetes.Clientset, deltaWork DeltaWork) error {
	slog.Info("applyDeltaWork commencing.", "deltaWork", deltaWork)
	var err error
	for _, pcInfo := range deltaWork.PriorityClassWork.ToDeploy {
		_, err = clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pcInfo.PriorityClass, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("applyDeltaWork cannot create the priority class %s: %w", pcInfo.Name, err)
		}
		slog.Info("applyDeltaWork successfully created the priority class", "pc.Name", pcInfo.Name)
	}
	err = createNamespaces(ctx, clientSet, deltaWork.NamespaceWork.ToCreate.UnsortedList()...)
	if err != nil {
		return fmt.Errorf("applyDeltaWork cannot create namespaces: %w", err)
	}

	podsToDelete := deltaWork.PodWork.ToDelete.UnsortedList()
	for _, p := range podsToDelete {
		err = clientSet.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			return err
		}
		slog.Info("applyDeltaWork successfully deleted the pod", "pod.Name", p.Name)
	}

	podsToDeploy := deltaWork.PodWork.ToDeploy

	// deploy kube-system pods and pods that have assigned Node names first.
	slices.SortFunc(podsToDeploy, apputil.SortPodInfoForDeployment)

	for i, podInfo := range podsToDeploy {
		pod := getCorePodFromPodInfo(podInfo)
		//if pod.Namespace != "default" {
		//	err = createNamespaces(ctx, clientSet, pod.Namespace)
		//	if err != nil && !apierrors.IsAlreadyExists(err) {
		//		return err
		//	}
		//}
		slog.Info("applyDeltaWork is deploying pod", "deployCount", i+1, "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "pod.NodeName", pod.Spec.NodeName)
		podNew, err := clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("applyDeltaWork cannot create the pod  %s: %w", pod.Name, err)
		}
		if podNew.Spec.NodeName != "" {
			podNew.Status.Phase = corev1.PodRunning
			_, err = clientSet.CoreV1().Pods(pod.Namespace).UpdateStatus(ctx, podNew, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("applyDeltaWork cannot change the pod Phase to Running for %s: %w", pod.Name, err)
			}
		}
	}

	err = deleteNamespaces(ctx, clientSet, deltaWork.NamespaceWork.ToDelete.UnsortedList()...)
	if err != nil {
		return fmt.Errorf("applyDeltaWork cannot delete un-used namespaces: %w", err)
	}
	slog.Info("applyDeltaWork success", "workCount", deltaWork.Number, "deltaWork", deltaWork)
	return nil
}

// applyDeltaWorkOld is dead code and can be removed later
func (d *defaultReplayer) applyDeltaWorkOld(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot) (workDone bool, err error) {

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

	err = createNamespaces(ctx, d.clientSet, nsToDeploy.UnsortedList()...)
	if err != nil {
		return
	}

	virtualPodsList, err := d.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	virtualPodsByName := lo.Associate(virtualPodsList.Items, func(item corev1.Pod) (string, gsc.PodInfo) {
		return item.Name, recorder.PodInfoFromPod(&item)
	})
	clusterSnapshotPodsByName := lo.KeyBy(clusterSnapshot.Pods, func(item gsc.PodInfo) string {
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
		if podInfo.NodeName != "" {

		}
		if err != nil {
			err = fmt.Errorf("cannot create the pod  %s: %w", pod.Name, err)
			return
		}
		workDone = true
	}
	return
}

func (d *defaultReplayer) getNextReplayMarkTime() (replayTime time.Time, err error) {
	if d.scaleUpEventCounter >= len(d.scaleUpEvents) {
		return
	}
	replayTime = d.scaleUpEvents[d.scaleUpEventCounter].EventTime
	d.scaleUpEventCounter++
	//if d.lastClusterSnapshot.SnapshotTime.IsZero() {
	//	//recordStartTime, err := d.dataAccess.GetInitialRecorderStartTime()
	//	//if err != nil {
	//	//	return
	//	//}
	//	//recordStartTime = recordStartTime.Add(d.params.StabilizeInterval).UTC()
	//	firstScaleUpEventTime := d.scaleUpEvents[0].EventTime
	//	//replayTime = firstScaleUpEventTime.Add(-d.params.StabilizeInterval).UTC()
	//	replayTime = firstScaleUpEventTime.UTC()
	//	return
	//}
	//replayTime = d.lastClusterSnapshot.SnapshotTime.UTC().Add(d.params.ReplayInterval).UTC()
	return
}

func (d *defaultReplayer) computeAndApplyDeltaWork(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot, pendingPods []corev1.Pod) (workDone bool, err error) {
	deltaWork, err := computeDeltaWork(ctx, d.clientSet, clusterSnapshot, pendingPods)
	if err != nil {
		return
	}
	if deltaWork.IsEmpty() {
		slog.Info("deltaWork is empty. Skipping applying work")
		return
	}
	d.workCount++
	deltaWork.Number = d.workCount
	err = applyDeltaWork(ctx, d.clientSet, deltaWork)
	if err != nil {
		return
	}
	workDone = true
	return
}

func computeDeltaWork(ctx context.Context, clientSet *kubernetes.Clientset, clusterSnapshot gsc.ClusterSnapshot, pendingPods []corev1.Pod) (deltaWork DeltaWork, err error) {
	slog.Info("computeDeltaWork for clusterSnapshot.", "clusterSnapshot.Number", clusterSnapshot.Number)
	pcWork, err := computePriorityClassWork(ctx, clientSet, clusterSnapshot.PriorityClasses)
	if err != nil {
		return
	}

	nsWork, err := computeNamespaceWork(ctx, clientSet, clusterSnapshot.GetPodNamspaces())
	if err != nil {
		return
	}

	podWork, err := computePodWork(ctx, clientSet, clusterSnapshot.Pods, pendingPods)
	if err != nil {
		return
	}
	deltaWork.Number = clusterSnapshot.Number
	deltaWork.PodWork = podWork
	deltaWork.PriorityClassWork = pcWork
	deltaWork.NamespaceWork = nsWork
	return
}

func computePodWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPods []gsc.PodInfo, pendingPods []corev1.Pod) (podWork PodWork, err error) {
	virtualNodes, err := clientutil.ListAllNodes(ctx, clientSet)
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}
	virtualNodeNames := lo.Map(virtualNodes, func(item corev1.Node, index int) string {
		return item.Name
	})
	podWork.ToDelete, err = getPodNamesNotAssignedToNodes(ctx, clientSet, virtualNodeNames)
	virtualPods, err := clientutil.ListAllPods(ctx, clientSet)
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	virtualPods = append(virtualPods, pendingPods...)
	virtualPodsByName := lo.Associate(virtualPods, func(item corev1.Pod) (string, gsc.PodInfo) {
		return item.Name, recorder.PodInfoFromPod(&item)
	})
	clusterSnapshotPodsByName := lo.KeyBy(snapshotPods, func(item gsc.PodInfo) string {
		return item.Name
	})

	for _, podInfo := range virtualPodsByName {
		snapshotPodInfo, ok := clusterSnapshotPodsByName[podInfo.Name]
		if ok && (snapshotPodInfo.NominatedNodeName == podInfo.NominatedNodeName || snapshotPodInfo.NodeName == podInfo.NodeName) {
			continue
		}
		podWork.ToDelete.Insert(types.NamespacedName{Namespace: podInfo.Namespace, Name: podInfo.Name})
		delete(virtualPodsByName, podInfo.Name)
	}

	for _, podInfo := range snapshotPods {
		_, ok := virtualPodsByName[podInfo.Name]
		if ok {
			continue
		}
		podWork.ToDeploy = append(podWork.ToDeploy, podInfo)
	}
	return
}

func computePriorityClassWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPCs []gsc.PriorityClassInfo) (pcWork PriorityClassWork, err error) {
	virtualPCs, err := clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the priority classes in virtual cluster: %w", err)
		return
	}
	virtualPCsMap := lo.KeyBy(virtualPCs.Items, func(item schedulingv1.PriorityClass) string {
		return item.Name
	})
	for _, pcInfo := range snapshotPCs {
		_, ok := virtualPCsMap[pcInfo.Name]
		if ok {
			continue
		}
		pcWork.ToDeploy = append(pcWork.ToDeploy, pcInfo)
	}
	return
}

func computeNamespaceWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPodNamespaces sets.Set[string]) (nsWork NamespaceWork, err error) {
	virtualNamespaces, err := getNamespaces(ctx, clientSet)
	if err != nil {
		return
	}
	nsWork.ToCreate = nsWork.ToCreate.Union(snapshotPodNamespaces.Difference(virtualNamespaces))
	// FIXME: uncomment me after recording all namespaces
	//nsWork.ToDelete.Union(virtualNamespaces.Difference(snapshotPodNamespaces))
	return
}

func getPodNamesNotAssignedToNodes(ctx context.Context, clientSet *kubernetes.Clientset, nodeNames []string) (podNames sets.Set[types.NamespacedName], err error) {
	nodeNameSet := sets.New[string](nodeNames...)
	podsList, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	podNames = make(sets.Set[types.NamespacedName])
	for _, pod := range podsList.Items {
		if nodeNameSet.Has(pod.Spec.NodeName) {
			continue
		}
		podNames.Insert(types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		})
	}
	return
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

func (d *defaultReplayer) Close() error {
	// TODO: clean up cluster can be done here.
	return d.dataAccess.Close()
}

func (d *defaultReplayer) GetRecordedClusterSnapshot(markTime time.Time) (cs gsc.ClusterSnapshot, err error) {
	d.snapshotCount++
	cs.Number = d.snapshotCount
	cs.SnapshotTime = markTime

	mccs, err := d.dataAccess.LoadMachineClassInfosBefore(markTime)
	if err != nil {
		return
	}

	mcds, err := d.dataAccess.LoadMachineDeploymentInfosBefore(markTime)
	if err != nil {
		return
	}
	workerPools, err := d.dataAccess.LoadWorkerPoolInfosBefore(markTime)
	if err != nil {
		return
	}
	cs.WorkerPools = workerPools

	cs.AutoscalerConfig.NodeTemplates, err = GetNodeTemplates(mccs, mcds)
	if err != nil {
		return
	}

	cs.PriorityClasses, err = d.dataAccess.LoadLatestPriorityClassInfoBeforeSnapshotTime(markTime)
	if err != nil {
		return
	}

	cs.Nodes, err = d.dataAccess.LoadNodeInfosBefore(markTime)
	if err != nil {
		err = fmt.Errorf("cannot get the node infos before markTime %q: %w", markTime, err)
		return
	}
	if len(cs.Nodes) == 0 {
		err = fmt.Errorf("no existingNodes available before markTime %q", markTime)
		return
	}

	cs.Pods, err = d.dataAccess.GetLatestPodInfosBeforeSnapshotTime(markTime)
	apputil.SortPodInfosForReadability(cs.Pods)

	cs.AutoscalerConfig.CASettings, err = d.dataAccess.LoadCASettingsBefore(markTime)
	if err != nil {
		return
	}
	cs.AutoscalerConfig.Mode = gsc.AutoscalerReplayerMode
	cs.AutoscalerConfig.ExistingNodes = cs.Nodes
	cs.AutoscalerConfig.NodeGroups, err = deriveNodeGroups(mcds, cs.AutoscalerConfig.CASettings.NodeGroupsMinMax)
	if err != nil {
		return
	}

	cs.AutoscalerConfig.Hash = cs.AutoscalerConfig.GetHash()
	cs.Hash = cs.GetHash()
	return
}

func (d *defaultReplayer) GetParams() gsh.ReplayerParams {
	//TODO implement me
	panic("implement me")
}

func getNodeGroupsByPoolZone(nodeGroupsByName map[string]gsc.NodeGroupInfo) map[gsh.PoolZone]gsc.NodeGroupInfo {
	poolZoneMap := make(map[gsh.PoolZone]gsc.NodeGroupInfo)
	for _, ng := range nodeGroupsByName {
		poolKey := gsh.PoolZone{
			PoolName: ng.PoolName,
			Zone:     ng.Zone,
		}
		poolZoneMap[poolKey] = ng
	}
	return poolZoneMap
}

func (d *defaultReplayer) appendScenario(scenario gsh.Scenario, clusterSnapshot gsc.ClusterSnapshot) error {
	if d.report == nil {
		d.report = &gsh.ReplayReport{
			StartTime: clusterSnapshot.SnapshotTime,
			Scenarios: make([]gsh.Scenario, 0),
		}
	}
	d.report.Scenarios = append(d.report.Scenarios, scenario)
	slog.Info("appended scenario for", "snapshotTime", clusterSnapshot.SnapshotTime, "scaledUpNodeGroups", scenario.ScalingResult.ScaledUpNodeGroups)
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

func (d *defaultReplayer) createScenario(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot) (scenario gsh.Scenario, err error) {
	postVirtualNodesList, err := d.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}

	var scaledUpNodes []gsc.NodeInfo
	postVirtualNodes := postVirtualNodesList.Items
	preVirtualNodesMap := lo.KeyBy(clusterSnapshot.Nodes, func(item gsc.NodeInfo) string {
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
		slog.Warn("no scale-up in this replay interval, so skipping this scenario", "snapshotTime", clusterSnapshot.SnapshotTime)
		err = ErrNoScenario
		return
	}
	scenario.BeginTime = clusterSnapshot.SnapshotTime.UTC()
	scenario.ClusterSnapshot = clusterSnapshot
	scenario.ScalingResult.ScaledUpNodes = scaledUpNodes
	scenario.ScalingResult.ScaledUpNodeGroups = make(map[string]int)
	poolZoneMap := getNodeGroupsByPoolZone(clusterSnapshot.AutoscalerConfig.NodeGroups)
	for _, node := range scaledUpNodes {
		poolZone := gsh.PoolZone{
			PoolName: node.Labels["worker.gardener.cloud/pool"],
			Zone:     node.Labels["topology.kubernetes.io/zone"],
		}
		ng, ok := poolZoneMap[poolZone]
		if !ok {
			err = fmt.Errorf("cannot find associated PoolZone %q for node %q", poolZone, node.Name)
			return
		}
		scenario.ScalingResult.ScaledUpNodeGroups[ng.Name]++
	}
	pods, err := clientutil.ListAllPods(ctx, d.clientSet)
	for _, pod := range pods {
		podInfo := recorder.PodInfoFromPod(&pod)
		// FIXME: BUGGY
		if podInfo.PodScheduleStatus == gsc.PodUnscheduled || podInfo.PodScheduleStatus == gsc.PodSchedulePending {
			scenario.ScalingResult.PendingUnscheduledPods = append(scenario.ScalingResult.PendingUnscheduledPods, podInfo)
		}
	}

	return
}

func GetNodeGroupNameFromMCCName(namespace, mccName string) string {
	idx := strings.LastIndex(mccName, "-")
	// mcc name - shoot--i585976--suyash-local-worker-1-z1-0af3f , we omit the hash from the mcc name to match it with the nodegroup name
	trimmedName := mccName[0:idx]
	return fmt.Sprintf("%s.%s", namespace, trimmedName)
}

func constructNodeTemplateFromMCC(mcc gsh.MachineClassInfo) gsc.NodeTemplate {
	return gsc.NodeTemplate{
		Name:         GetNodeGroupNameFromMCCName(mcc.Namespace, mcc.Name),
		Capacity:     mcc.Capacity,
		InstanceType: mcc.InstanceType,
		Region:       mcc.Region,
		Zone:         mcc.Zone,
		Labels:       mcc.Labels,
		Taints:       nil,
	}
}

func GetNodeTemplates(mccs []gsh.MachineClassInfo, mcds []gsc.MachineDeploymentInfo) (nodeTemplates map[string]gsc.NodeTemplate, err error) {
	nodeTemplates = make(map[string]gsc.NodeTemplate)
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

func deriveNodeGroups(mcds []gsc.MachineDeploymentInfo, ngMinMaxMap map[string]gsc.MinMax) (nodeGroups map[string]gsc.NodeGroupInfo, err error) {
	nodeGroups = make(map[string]gsc.NodeGroupInfo)
	for _, mcd := range mcds {
		ngName := fmt.Sprintf("%s.%s", mcd.Namespace, mcd.Name)
		minMax, ok := ngMinMaxMap[ngName]
		if !ok {
			err = fmt.Errorf("cannot find NodeGroup with name %q in ngMinMaxMap map %q", ngName, ngMinMaxMap)
		}
		nodeGroup := gsc.NodeGroupInfo{
			Name:       ngName,
			PoolName:   mcd.PoolName,
			Zone:       mcd.Zone,
			TargetSize: mcd.Replicas,
			MinSize:    minMax.Min,
			MaxSize:    minMax.Max,
		}
		nodeGroup.Hash = nodeGroup.GetHash()
		nodeGroups[nodeGroup.Name] = nodeGroup
	}
	return
}

func deletePendingUnscheduledPods(ctx context.Context, clientSet *kubernetes.Clientset) (deletedPods []corev1.Pod, err error) {
	pods, err := clientutil.ListAllPods(ctx, clientSet)
	if err != nil {
		return
	}
	for _, p := range pods {
		ss := recorder.ComputePodScheduleStatus(&p)
		if ss == gsc.PodUnscheduled {
			err = clientSet.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{
				GracePeriodSeconds: pointer.Int64(0),
			})
			if err != nil && !apierrors.IsNotFound(err) {
				return
			}
			deletedPods = append(deletedPods, p)
			slog.Info("Deleted pending unscheduled pod before resetting autoscaler config", "pod.Name", p.Name)
		}
	}
	return
}

func waitTillNodesStarted(ctx context.Context, clientSet *kubernetes.Clientset, snapshotNumber int, numWaitNodes int) error {
	for {
		select {
		case <-ctx.Done():
			slog.Warn("Context cancelled or timed out:", ctx.Err())
			return ctx.Err()
		default:
			slog.Info("waitTillNodesStarted listing nodes of virtual cluster", "snapshotNumber", snapshotNumber, "numWaitNodes", numWaitNodes)
			nodes, err := clientutil.ListAllNodes(ctx, clientSet)
			if err != nil {
				return fmt.Errorf("waitTillNodesStarted for %d nodes got error: %w", numWaitNodes, err)
			}
			numRunningNodes := 0
			for _, n := range nodes {
				if n.Status.Phase == corev1.NodeRunning {
					numRunningNodes++
				}
			}
			if numRunningNodes >= numWaitNodes {
				slog.Info("waitTillNodesStarted has reached required numRunningNodes", "numRunningNodes", numRunningNodes)
				return nil
			} else {
				slog.Info("waitTillNodesStarted has not yet reached required numWaitNodes",
					"snapshotNumber", snapshotNumber,
					"numRunningNodes", numRunningNodes,
					"numWaitNodes", numWaitNodes)
			}
			<-time.After(2 * time.Second)
		}
	}
}
