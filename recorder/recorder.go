package recorder

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	policyv1informers "k8s.io/client-go/informers/policy/v1"
	schedulingv1informers "k8s.io/client-go/informers/scheduling/v1"
	storagev1informers "k8s.io/client-go/informers/storage/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	//"sync"
	"sync/atomic"
	"time"
)

const CLUSTERS_CFG_FILE = "clusters.csv"

var machineDeploymentGVR = schema.GroupVersionResource{Group: "machine.sapcloud.io", Version: "v1alpha1", Resource: "machinedeployments"}
var machineClassGVR = schema.GroupVersionResource{Group: "machine.sapcloud.io", Version: "v1alpha1", Resource: "machineclasses"}
var workerGVR = schema.GroupVersionResource{Group: "extensions.gardener.cloud", Version: "v1alpha1", Resource: "workers"}
var deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
var configmapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
var eventGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

var caOptions = sets.New("expander", "max-nodes-total", "max-graceful-termination-sec", "max-node-provision-time", "scan-interval", "ignore-daemonsets-utilization", "new-pod-scale-up-delay", "max-empty-bulk-delete")
var ErrKeyNotFound = errors.New("key not found")

const podTriggerScaleUpPattern = `.*(shoot--\S+) (\d+)\->(\d+) .*max: (\d+).*`
const machineSetScaleUpPattern = `Scaled up.*? to (\d+)`

var podTriggeredScaleUpRegex = regexp.MustCompile(podTriggerScaleUpPattern)
var machineSetScaleUpRegex = regexp.MustCompile(machineSetScaleUpPattern)

type recorderKey struct {
	Landscape   string
	ProjectName string
	ShootName   string
}

var recordersMap = make(map[recorderKey]*defaultRecorder)
var recorderCheckStarted atomic.Bool
var checkNum = 0

func startRecorderCheck(ctx context.Context) {
	if !recorderCheckStarted.CompareAndSwap(false, true) {
		return
	}
	for {
		checkNum++
		select {
		case <-ctx.Done():
			slog.Info("startRecorderCheck exiting since serverCtx is done")
			return
		case <-time.After(3 * time.Minute):
			slog.Info("startRecorderCheck commencing", "checkNum", checkNum)
			for _, recorder := range recordersMap {
				err := recorder.connChecker.TestConnection(recorder.ctx)
				if err == nil {
					slog.Info("startRecorderCheck PASSED connection test for recorder", "checkNum", checkNum, "recorderParams", recorder.params)
					continue
				}
				slog.Error("startRecorderCheck FAILED connection test for recorder", "checkNum", checkNum, "error", err, "recorderParams", recorder.params)
				connError := errors.Is(err, net.ErrClosed)
				authError := apierrors.IsUnauthorized(err)
				if !authError && !connError {
					slog.Error("startRecorderCheck skipping re-init test for recorder since neither conn nor auth error", "checkNum", checkNum, "error", err, "recorderParams", recorder.params)
					continue
				}
				slog.Warn("startRecorderCheck is STOPPING old recorder and RECREATING fresh recorder after unauthorized error", "checkNum", checkNum, "recorderParams", recorder.params)
				//			slog.Warn("startRecorderCheck determined Unauthorized error for recorder", "recorder", recorder.params)
				_ = recorder.Close()
				freshParams, err := createFreshRecorderParams(ctx, recorder.params)
				freshRecorder, err := NewDefaultRecorder(ctx, freshParams, recorder.startTime)
				if err != nil {
					slog.Error("startRecorderCheck cannot CREATE fresh recorder after conn auth failure", "checkNum", checkNum, "recorderParams", recorder.params)
				}
				err = freshRecorder.Start()
				if err != nil {
					slog.Error("startRecorderCheck cannot START fresh recorder", "checkNum", checkNum, "error", err, "recorderParams", recorder.params)
				}
			}
		}
	}
}

func createFreshRecorderParams(ctx context.Context, oldParams gsh.RecorderParams) (params gsh.RecorderParams, err error) {
	landscapeKubeConfigs, err := apputil.GetLandscapeKubeconfigs(oldParams.Mode)
	if err != nil {
		return
	}
	landscapeKubeconfig, ok := landscapeKubeConfigs[oldParams.Landscape]
	if !ok {
		err = fmt.Errorf("cannot find kubeconfig for landscape %q", oldParams.Landscape)
		return
	}
	landscapeClient, err := apputil.CreateLandscapeClient(landscapeKubeconfig, oldParams.Mode)
	if err != nil {
		err = fmt.Errorf("cannot create landscape client for landscape %q, projectName %q, shootName %q: %w", oldParams.Landscape, oldParams.ProjectName, oldParams.ShootName, err)
		return
	}
	shootKubeconfigPath, err := apputil.GetViewerKubeconfig(ctx, landscapeClient, oldParams.Landscape, oldParams.ProjectName, oldParams.ShootName)
	if err != nil {
		err = fmt.Errorf("cannot get viewer kubeconfig for shoot %q: %w", oldParams.ShootName, err)
		return
	}

	seedKubeconfigPath, err := apputil.GetViewerKubeconfig(ctx, landscapeClient, oldParams.Landscape, "garden", oldParams.SeedName)
	if err != nil {
		err = fmt.Errorf("cannot get viewer kubeconfig for seed %q: %w", oldParams.SeedName, err)
		return
	}
	params = gsh.RecorderParams{
		Mode:                oldParams.Mode,
		Landscape:           oldParams.Landscape,
		ProjectName:         oldParams.ProjectName,
		ShootName:           oldParams.ShootName,
		SeedName:            oldParams.SeedName,
		ShootNameSpace:      oldParams.ShootNameSpace,
		ShootKubeConfigPath: shootKubeconfigPath,
		SeedKubeConfigPath:  seedKubeconfigPath,
		DBDir:               oldParams.DBDir,
	}
	return
}

func NewDefaultRecorder(parentCtx context.Context, params gsh.RecorderParams, startTime time.Time) (gsh.Recorder, error) {
	// Load kubeconfig file
	rCtx, rCancelCauseFunc := context.WithCancelCause(parentCtx)
	config, err := clientcmd.BuildConfigFromFlags("", params.ShootKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create shoot clientset: %w", err)
	}

	controlConfig, err := clientcmd.BuildConfigFromFlags("", params.SeedKubeConfigPath)
	if err != nil {
		slog.Error("cannot create the client config for the control plane", "error", err)
		return nil, fmt.Errorf("cannot create the client config for the control plane: %w", err)
	}
	connChecker, err := NewConnChecker(config, controlConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create the conn checker for recorder %q: %w", params, err)
	}

	controlClientSet, err := dynamic.NewForConfig(controlConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset for control plane: %w", err)
	}

	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	controlInformerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(controlClientSet, 0, params.ShootNameSpace, nil)
	dataDBName := strings.TrimSuffix(strings.TrimPrefix(path.Base(params.ShootKubeConfigPath), "kubeconfig-"), ".yaml") + ".db"
	dataDBPath := path.Join(params.DBDir, dataDBName)
	slog.Info("Building Recorder", "recorderParams", params, "dataDBPath", dataDBPath)
	recorderInstance := &defaultRecorder{params: params,
		ctx:                    rCtx,
		cancelFunc:             rCancelCauseFunc,
		connChecker:            connChecker,
		startTime:              startTime,
		informerFactory:        informerFactory,
		eventsInformer:         informerFactory.Core().V1().Events(),
		controlEventsInformer:  controlInformerFactory.ForResource(eventGVR),
		podsInformer:           informerFactory.Core().V1().Pods(),
		pcInformer:             informerFactory.Scheduling().V1().PriorityClasses(),
		pdbInformer:            informerFactory.Policy().V1().PodDisruptionBudgets(),
		nodeInformer:           informerFactory.Core().V1().Nodes(),
		csiInformer:            informerFactory.Storage().V1().CSINodes(),
		controlInformerFactory: controlInformerFactory,
		mcdInformer:            controlInformerFactory.ForResource(machineDeploymentGVR),
		mccInformer:            controlInformerFactory.ForResource(machineClassGVR),
		deploymentInformer:     controlInformerFactory.ForResource(deploymentGVR),
		configmapInformer:      informerFactory.Core().V1().ConfigMaps(),
		workerInformer:         controlInformerFactory.ForResource(workerGVR),
		dataAccess:             db.NewDataAccess(dataDBPath),
	}
	recKey := recorderKey{
		Landscape:   params.Landscape,
		ProjectName: params.ProjectName,
		ShootName:   params.ShootName,
	}
	recordersMap[recKey] = recorderInstance
	go startRecorderCheck(parentCtx)
	return recorderInstance, nil
}

var _ gsh.Recorder = (*defaultRecorder)(nil)

type defaultRecorder struct {
	params                 gsh.RecorderParams
	ctx                    context.Context
	cancelFunc             context.CancelCauseFunc
	started                bool
	connChecker            *ConnChecker
	startTime              time.Time
	informerFactory        informers.SharedInformerFactory
	eventsInformer         corev1informers.EventInformer
	controlEventsInformer  informers.GenericInformer
	podsInformer           corev1informers.PodInformer
	pcInformer             schedulingv1informers.PriorityClassInformer
	pdbInformer            policyv1informers.PodDisruptionBudgetInformer
	nodeInformer           corev1informers.NodeInformer
	csiInformer            storagev1informers.CSINodeInformer
	controlInformerFactory dynamicinformer.DynamicSharedInformerFactory
	mcdInformer            informers.GenericInformer
	mccInformer            informers.GenericInformer
	workerInformer         informers.GenericInformer
	deploymentInformer     informers.GenericInformer
	configmapInformer      corev1informers.ConfigMapInformer
	dataAccess             *db.DataAccess
	//nodeAllocatableVolumes sync.Map
	stopCh <-chan struct{}
}

func (r *defaultRecorder) IsStarted() bool {
	return r.started
}

func (r *defaultRecorder) Close() error {
	r.cancelFunc(errors.New("recorder closed"))
	return nil
}

func (r *defaultRecorder) doClose() error {
	err := r.dataAccess.Close()
	if err != nil {
		return err
	}
	r.informerFactory.Shutdown()
	r.controlInformerFactory.Shutdown()
	return nil
}

var errCount = 0

func GetInnerMapValue(parentMap map[string]any, keys ...string) (any, error) {
	subkeys := keys[:len(keys)-1]
	childMap, err := GetInnerMap(parentMap, subkeys...)
	if err != nil {
		return nil, err
	}
	val, ok := childMap[keys[len(keys)-1]]
	if !ok {
		return nil, fmt.Errorf("could not find value for keys %q : %w", keys, ErrKeyNotFound)
	}
	return val, nil
}

func GetInnerMap(parentMap map[string]any, keys ...string) (map[string]any, error) {
	var mapPath []string
	childMap := parentMap
	for _, k := range keys {
		mapPath = append(mapPath, k)
		mp, ok := childMap[k]
		if !ok {
			return nil, fmt.Errorf("cannot find the child map under mapPath: %s", mapPath)
		}
		childMap, ok = mp.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("child map is not of type map[string] any under the mapPath: %s", mapPath)
		}
	}
	return childMap, nil
}

func (r *defaultRecorder) onAddPod(obj any) {
	if obj == nil {
		return
	}
	podNew := obj.(*corev1.Pod)
	slog.Debug("onAddPod.", "podName", podNew.Name, "podNew.UID", podNew.UID, "podNew.UID", podNew.UID, "shootLabel", r.params.ShootLabel())
	err := r.processPod(nil, podNew)
	if err != nil {
		slog.Error("onAddPod failed", "error", err, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) processPC(pcOld, pcNew *schedulingv1.PriorityClass) error {
	if pcNew.DeletionTimestamp != nil {
		// ignore deletes and pod with no node
		return nil
	}
	pcInfo := pcInfoFromPC(pcNew)

	pcCountWithSpecHash, err := r.dataAccess.CountPCInfoWithSpecHash(pcNew.Name, pcInfo.Hash)
	if err != nil {
		slog.Error("CountPCInfoWithSpecHash failed", "error", err, "pc.Name", pcNew.Name, "pc.uid", pcNew.UID, "pc.hash", pcInfo.Hash, "recorderParams", r.params)
		return err
	}

	if pcCountWithSpecHash > 0 {
		slog.Debug("pc is already inserted with hash", "pc.Name", pcNew.Name, "pc.uid", pcNew.UID, "pc.Hash", pcInfo.Hash)
		return err
	}

	_, err = r.dataAccess.StorePriorityClassInfo(pcInfo)
	if err != nil {
		slog.Error("could not execute pc_info insert", "error", err, "pod.Name", pcInfo.Name, "pod.UID", pcInfo.UID, "pod.CreationTimestamp", pcInfo.CreationTimestamp, "pod.Hash", pcInfo.Hash, "recorderParams", r.params)
		return err
	}
	return nil
}

func (r *defaultRecorder) onUpdatePC(old, new any) {
	if old == nil || new == nil {
		return
	}
	pcNew := new.(*schedulingv1.PriorityClass)
	slog.Info("PC obj changed.", "pcNew", pcNew.GetName(), "PC.Generation", pcNew.GetGeneration())
	pcOld := old.(*schedulingv1.PriorityClass)
	slog.Debug("onUpdatepc.", "pcName", pcOld.Name, "pcOld.UID", pcOld.UID, "pcNew.UID", pcNew.UID)
	err := r.processPC(pcOld, pcNew)
	if err != nil {
		slog.Error("onUpdatePod failed", "error", err, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) onAddPC(obj any) {
	if obj == nil {
		return
	}
	pcNew := obj.(*schedulingv1.PriorityClass)
	slog.Info("onAddPC.", "pcName", pcNew.Name, "pcNew.UID", pcNew.UID, "pcNew.Value", pcNew.Value, "pcNew.PreemptionPolicy", pcNew.PreemptionPolicy)
	err := r.processPC(nil, pcNew)
	if err != nil {
		slog.Error("onAddPC failed", "error", err, "recorderParams", r.params)
		return
	}
}

func IsOwnedBy(pod *corev1.Pod, gvks []schema.GroupVersionKind) bool {
	for _, ignoredOwner := range gvks {
		for _, owner := range pod.ObjectMeta.OwnerReferences {
			if owner.APIVersion == ignoredOwner.GroupVersion().String() && owner.Kind == ignoredOwner.Kind {
				return true
			}
		}
	}
	return false
}

func (r *defaultRecorder) onUpdatePod(old, new any) {
	if old == nil || new == nil {
		return
	}
	podNew := new.(*corev1.Pod)
	podOld := old.(*corev1.Pod)
	slog.Debug("onUpdatePod.", "podName", podOld.Name, "podOld.UID", podOld.UID, "podNew.UID", podNew.UID, "shootLabel", r.params.ShootLabel())
	err := r.processPod(podOld, podNew)
	if err != nil {
		slog.Error("onUpdatePod failed", "error", err, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) onDeletePod(obj any) {
	if obj == nil {
		return
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			slog.Error("onDeletePod got an obj that is neither pod nor cache.DeletedFinalStateUnknown", "object", obj)
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			slog.Error("Tombstone contained object that is not a Pod", "object", obj, "recorderParams", r.params)
			return
		}
	}
	if pod.DeletionTimestamp == nil {
		return //sometimes this handler is invoked with null deletiontimestamp!
	}
	rowsUpdated, err := r.dataAccess.UpdatePodDeletionTimestamp(pod.UID, pod.DeletionTimestamp.Time.UTC())
	slog.Debug("updated deletionTimestamp of pod", "pod.name", pod.Name, "pod.uid", pod.UID, "pod.deletionTimestamp", pod.DeletionTimestamp, "rows.updated", rowsUpdated)
	if err != nil {
		slog.Error("could not execute pod deletion timestamp update", "error", err)
	}
}

func (r *defaultRecorder) onAddNode(obj interface{}) {
	r.processNode(nil, obj)
}

func (r *defaultRecorder) onUpdateNode(old, new any) {
	r.processNode(old, new)
}

func (r *defaultRecorder) processNode(old any, new any) {
	nodeNew := new.(*corev1.Node)
	if nodeNew.DeletionTimestamp != nil {
		// ignore deletes
		return
	}
	var nodeOld *corev1.Node
	if nodeOld != nil {
		nodeOld = old.(*corev1.Node)
	}
	if !apputil.IsNodeReady(nodeNew) {
		slog.Debug("processNode is skipping node since node not Ready.", "nodeName", nodeNew.Name, "shootLabel", r.params.ShootLabel())
		return
	}
	//	allocatableVolumes := r.getAllocatableVolumes(nodeNew.Name)
	nodeNewInfo := gsh.NodeInfoFromNode(nodeNew, 0)
	//if allocatableVolumes == 0 {
	//	slog.Warn("Allocatable Volumes key not found", "node.Name", nodeNewInfo.Name)
	//}
	countWithSameHash, err := r.dataAccess.CountNodeInfoWithHash(nodeNew.Name, nodeNewInfo.Hash)
	if err != nil {
		slog.Error("cannot CountPodInfoWithSpecHash", "node.Name", nodeNew.Name, "node.Hash", nodeNewInfo.Hash, "error", err, "shootLabel", r.params.ShootLabel())
		return
	}
	if countWithSameHash > 0 {
		slog.Debug("NodeInfo is already present with same hash", "node.Name", nodeNew.Name, "node.Hash", nodeNewInfo.Hash, "count", countWithSameHash, "error", err)
		return
	}
	_, err = r.dataAccess.StoreNodeInfo(nodeNewInfo)
	if err != nil {
		slog.Error("could not store node info.", "node.Name", nodeNew.Name, "error", err, "shootLabel", r.params.ShootLabel())
		return
	}
	slog.Info("processNode stored node", "node.Name", nodeNewInfo.Name, "node.Hash", nodeNewInfo.Hash, "shootLabel", r.params.ShootLabel())
}

//func (r *defaultRecorder) getAllocatableVolumes(nodeName string) (allocatableVolumes int) {
//	val, _ := r.nodeAllocatableVolumes.Load(nodeName)
//	if val != nil {
//		allocatableVolumes = val.(int)
//	}
//	return
//}

func InvokeOrScheduleFunc[T any](label string, duration time.Duration, entity T, fn func(T) error) {
	err := fn(entity)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			time.AfterFunc(duration, func() { InvokeOrScheduleFunc(label, duration, entity, fn) })
		} else {
			slog.Info("InvokeOrScheduleFunc", "label", label, "error", err)
		}
	}
}

func (r *defaultRecorder) onDeleteNode(obj any) {
	if obj == nil {
		return
	}
	node, ok := obj.(*corev1.Node)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			slog.Error("onDeleteNode got an obj that is neither node nor cache.DeletedFinalStateUnknown", "object", obj, "recorderParams", r.params)
			return
		}
		node, ok = tombstone.Obj.(*corev1.Node)
		if !ok {
			slog.Error("Tombstone contained object that is not a Node", "object", obj, "recorderParams", r.params)
			return
		}
	}
	delTimeStamp := time.Now().UTC() // shitty issue where sometimes >node.DeletionTimestamp is nil
	if node.DeletionTimestamp != nil {
		delTimeStamp = node.DeletionTimestamp.UTC()
	}
	rowsUpdated, err := r.dataAccess.UpdateNodeInfoDeletionTimestamp(node.Name, delTimeStamp)
	slog.Debug("updated DeletionTimestamp of Node.", "node.Name", node.Name, "node.DeletionTimestamp", delTimeStamp, "rows.updated", rowsUpdated)
	if err != nil {
		slog.Error("could not execute UpdateNodeInfoDeletionTimestamp ", "error", err, "node.Name", node.Name, "node.DeletionTimestamp", delTimeStamp, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) onAddEvent(obj any) {
	event := obj.(*corev1.Event)
	isCAEvent := event.Source.Component == "cluster-autoscaler" || event.ReportingController == "cluster-autoscaler"
	isSchedulerEvent := strings.Contains(event.Source.Component, "scheduler") ||
		strings.Contains(event.ReportingController, "scheduler")
	isTriggerScaleUp := strings.Contains(event.Reason, "TriggeredScaleUp")
	//isScaledUpNodeGroupEvent := strings.Contains(event.Reason, "ScaledUpGroup")
	isNodeControllerEvent := strings.Contains(event.ReportingController, "node-controller")
	var eventTime time.Time
	if !event.EventTime.IsZero() {
		eventTime = event.EventTime.Time.UTC()
	} else if !event.FirstTimestamp.IsZero() {
		eventTime = event.FirstTimestamp.Time.UTC()
	} else if !event.LastTimestamp.IsZero() {
		eventTime = event.LastTimestamp.Time.UTC()
	} else {
		slog.Warn("event has zero timestamp.", "event.UID", event.UID, "event.Message", event.Message)
		eventTime = eventTime.UTC()
	}
	if eventTime.Before(r.startTime) {
		return
	}
	if isTriggerScaleUp {
		slog.Info("onAddEvent: TriggeredScaleUp.", "event.Message", event.Message, "event.CreationTimestamp", event.CreationTimestamp)
	}
	var reportingController string
	if event.ReportingController != "" {
		reportingController = event.ReportingController
	} else {
		reportingController = event.Source.Component
	}

	eventInfo := gsc.EventInfo{
		UID:                     string(event.UID),
		EventTime:               eventTime,
		ReportingController:     reportingController,
		Reason:                  event.Reason,
		Message:                 event.Message,
		InvolvedObjectKind:      event.InvolvedObject.Kind,
		InvolvedObjectName:      event.InvolvedObject.Name,
		InvolvedObjectNamespace: event.InvolvedObject.Namespace,
		InvolvedObjectUID:       string(event.InvolvedObject.UID),
	}
	if isCAEvent || isSchedulerEvent || isNodeControllerEvent {
		err := r.dataAccess.StoreEventInfo(eventInfo)
		if err != nil {
			slog.Error("could not execute event insert", "error", err, "recorderParams", r.params)
			errCount++
		}
	}
}

func parseMachineSetScaleUpMessage(msg string) (targetSize int, err error) {
	groups := machineSetScaleUpRegex.FindStringSubmatch(msg)
	targetSize, err = strconv.Atoi(groups[1])
	if err != nil {
		return
	}
	return
}

func (r *defaultRecorder) getAllWorkerPoolHashes(workerOld *unstructured.Unstructured) (map[string]string, error) {
	var oldPoolInfoHashes = make(map[string]string)
	if workerOld != nil {
		oldPoolInfos, err := gsh.WorkerPoolInfosFromUnstructured(workerOld)
		if err != nil {
			return nil, fmt.Errorf("cannot parse worker pools for worker %q of generation %d: %w", workerOld.GetName(), workerOld.GetGeneration(), err)
		}
		oldPoolInfoHashes = lo.MapValues(oldPoolInfos, func(value gsc.WorkerPoolInfo, key string) string {
			return value.Hash
		})
	}
	hashesFromDb, err := r.dataAccess.LoadAllWorkerPoolInfoHashes()
	if err != nil {
		return nil, err
	}
	maps.Copy(oldPoolInfoHashes, hashesFromDb)
	return oldPoolInfoHashes, nil
}

// processWorker has a TODO: should ideally leverage a generic helper method.
func (r *defaultRecorder) processWorker(workerOld, workerNew *unstructured.Unstructured) error {
	newPoolInfos, err := gsh.WorkerPoolInfosFromUnstructured(workerNew)
	if err != nil {
		return fmt.Errorf("cannot parse worker pools for worker %q of generation %d: %w", workerNew.GetName(), workerNew.GetGeneration(), err)
	}
	oldPoolInfoHashes, err := r.getAllWorkerPoolHashes(workerOld)
	if err != nil {
		slog.Error("Error loading worker pool hashes", "error", err, "recorderParams", r.params)
		return err
	}
	for name, poolNew := range newPoolInfos {
		existingHash, ok := oldPoolInfoHashes[name]
		if ok && existingHash == poolNew.Hash {
			slog.Debug("Skipping store of poolNew since it has same hash as poolOld.", "Name", name, "Hash", poolNew.Hash)
			continue
		}
		_, err = r.dataAccess.StoreWorkerPoolInfo(poolNew)
		if err != nil {
			return fmt.Errorf("cannot store WorkerPoolInfo %q: %w", poolNew, err)
		}
	}
	return nil
}

func (r *defaultRecorder) processPod(podOld, podNew *corev1.Pod) error {
	if podNew.DeletionTimestamp != nil {
		// ignore deletes and pod with no node
		return nil
	}
	//if podNew.Status.Phase != corev1.PodRunning {
	//	slog.Debug("pod is not in Running phase, skipping persisting it", "pod.UID", podNew.UID, "pod.Name", podNew.Name, "pod.Phase", podNew.Status.Phase)
	//	return nil
	//}
	podInfo := PodInfoFromPod(podNew)
	if podInfo.PodScheduleStatus == gsc.PodSchedulePending {
		slog.Debug("pod is in PodSchedulePending state, skipping persisting it", "pod.UID", podInfo.UID, "pod.Name", podInfo.Name)
		return nil
	}

	podCountWithSpecHash, err := r.dataAccess.CountPodInfoWithSpecHash(string(podNew.UID), podInfo.Hash)
	if err != nil {
		slog.Error("CountPodInfoWithSpecHash failed", "error", err, "pod.Name", podNew.Name, "pod.uid", podNew.UID, "pod.hash", podInfo.Hash)
		return err
	}

	if podCountWithSpecHash > 0 {
		slog.Debug("pod is already inserted with hash", "pod.Name", podNew.Name, "pod.uid", podNew.UID, "pod.nodeName", podNew.Spec.NodeName, "pod.Hash", podInfo.Hash)
		return err
	}

	_, err = r.dataAccess.StorePodInfo(podInfo)
	if err != nil {
		slog.Error("could not execute pod_info insert", "error", err, "pod.Name", podInfo.Name, "pod.UID", podInfo.UID, "pod.CreationTimestamp", podInfo.CreationTimestamp, "pod.Hash", podInfo.Hash)
		return err
	}
	slog.Debug("processPod stored pod.", "pod.Name", podInfo.Name, "pod.UID", podInfo.UID, "pod.CreationTimestamp", podInfo.CreationTimestamp, "shootLabel", r.params.ShootLabel())
	return nil
}

func (r *defaultRecorder) onAddWorker(obj interface{}) {
	worker := obj.(*unstructured.Unstructured)
	slog.Info("Worker obj added.", "worker", worker.GetName(), "worker.Generation", worker.GetGeneration())
	err := r.processWorker(nil, worker)
	if err != nil {
		slog.Error("onAddWorker failed", "error", err)
	}
}

func (r *defaultRecorder) onUpdateWorker(old, new any) {
	workerNew := new.(*unstructured.Unstructured)
	slog.Info("Worker obj changed.", "workerNew", workerNew.GetName(), "workerNew.Generation", workerNew.GetGeneration())
	if new == nil {
		slog.Error("onUpdateWorker: new workerNew is nil")
		return
	}
	workerOld := old.(*unstructured.Unstructured)
	err := r.processWorker(workerOld, workerNew)
	if err != nil {
		slog.Error("onUpdateWorker failed", "error", err, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) onAddPDB(pdbObj any) {
	//TODO: PDB
	//pdb := pdbObj.(*policyv1.PodDisruptionBudget)
	//pdbSpecJSON, err := json.Marshal(pdb.Spec)
	//if err != nil {
	//	slog.Error("cannot parse the pdb spec to json", "error", err)
	//	return
	//}
	//_, err = r.pdbInsertStmt.Exec(pdb.UID, pdb.Name, pdb.Generation, pdb.CreationTimestamp.Time, pdb.Spec.MinAvailable.String(), pdb.Spec.MaxUnavailable.String(), pdbSpecJSON)
	//if err != nil {
	//	slog.Error("cannot insert the pdb in pdb_info table", "error", err)
	//}
	//slog.Info("successfully inserted pdb in pdb_info on add pdb event", "pdb.uid", pdb.UID, "pdb.name", pdb.Name)
}

func (r *defaultRecorder) onUpdatePDB(_, newPdbObj any) {
	//newPdb := newPdbObj.(*policyv1.PodDisruptionBudget)
	//oldGen, err := r.dataAccess.GetMaxPDBGeneration(string(newPdb.UID))
	//if err != nil {
	//	slog.Error("cannot get the max generation of pdb from db", "error", err)
	//	return
	//}
	//if int(newPdb.GetGeneration()) <= oldGen {
	//	slog.Info("no update required in db for the pdb update event", "pdb.uid", newPdb.UID, "pdb.Name", newPdb.Name)
	//	return
	//}
	//pdbSpecJSON, err := json.Marshal(newPdb.Spec)
	//if err != nil {
	//	slog.Error("cannot parse the pdb spec to json", "error", err)
	//	return
	//}
	//
	//_, err = r.pdbInsertStmt.Exec(newPdb.UID, newPdb.Name, newPdb.Generation, newPdb.CreationTimestamp.Time, newPdb.Spec.MinAvailable.String(), newPdb.Spec.MaxUnavailable.String(), pdbSpecJSON)
	//if err != nil {
	//	slog.Error("cannot insert the pdb in pdb_info table", "error", err)
	//}
	//slog.Info("successfully inserted the pdb on update pdb event", "pdb.uid", newPdb.UID, "pdb.name", newPdb.Name)
}

func (r *defaultRecorder) onDeletePDB(pdbObj any) {
	//pdb := pdbObj.(*policyv1.PodDisruptionBudget)
	////TODO : deletion timestamp for pdb is coming nil , giving time.Now currently.
	//_, err := r.updatePdbDeletionTimeStamp.Exec(time.Now(), pdb.UID)
	//if err != nil {
	//	slog.Error("cannot updated the deletion timestamp for pdb", "error", err)
	//}
}

func (r *defaultRecorder) Start() error {
	err := r.connChecker.TestConnection(r.ctx)
	if err != nil {
		return fmt.Errorf("conn failed connection test for recorder %q: %w", r.params, err)
	}
	err = r.dataAccess.Init()
	if err != nil {
		return err
	}
	err = r.dataAccess.InsertRecorderStartTime(r.startTime)
	if err != nil {
		return err
	}
	_, err = r.eventsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: r.onAddEvent,
	})
	if err != nil {
		return fmt.Errorf("cannot add event handler on eventsInformer: %w", err)
	}

	_, err = r.workerInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddWorker,
		UpdateFunc: r.onUpdateWorker,
	})

	if err != nil {
		return fmt.Errorf("cannot add event handler on workerInformer: %w", err)
	}
	_, err = r.podsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddPod,
		UpdateFunc: r.onUpdatePod,
		DeleteFunc: r.onDeletePod,
	})
	if err != nil {
		return fmt.Errorf("cannot add event handler on podsInformer: %w", err)
	}

	_, err = r.pcInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddPC,
		UpdateFunc: r.onUpdatePC,
	})
	if err != nil {
		return fmt.Errorf("cannot add event handler on pcInformer: %w", err)
	}
	_, err = r.pdbInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddPDB,
		UpdateFunc: r.onUpdatePDB,
		DeleteFunc: r.onDeletePDB,
	})
	if err != nil {
		return fmt.Errorf("cannot add event handlers on pdbInformer: %w", err)
	}

	_, err = r.nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddNode,
		UpdateFunc: r.onUpdateNode,
		DeleteFunc: r.onDeleteNode,
	})
	if err != nil {
		return fmt.Errorf("cannot add event handlers on pdbInformer: %w", err)
	}

	_, err = r.mcdInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddMCD,
		UpdateFunc: r.onUpdateMCD,
		DeleteFunc: r.onDeleteMCD,
	})

	_, err = r.mccInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddMCC,
		UpdateFunc: r.onUpdateMCC,
		DeleteFunc: r.onDeleteMCC,
	})

	_, err = r.controlEventsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: r.onAddControlEvent,
	})

	_, err = r.deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddDeployment,
		UpdateFunc: r.onUpdateDeployment,
	})

	_, err = r.configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddConfigMap,
		UpdateFunc: r.onUpdateConfigMap,
	})

	_, err = r.csiInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.onAddCSINode,
		UpdateFunc: r.onUpdateCSINode,
		DeleteFunc: r.onDeleteCSINode,
	})

	stopCh := r.ctx.Done()
	r.stopCh = stopCh
	r.runInformers(stopCh)

	slog.Info("Waiting for caches to be synced...")
	if !cache.WaitForCacheSync(r.ctx.Done(),
		r.deploymentInformer.Informer().HasSynced,
		r.configmapInformer.Informer().HasSynced,
		r.mcdInformer.Informer().HasSynced,
		r.podsInformer.Informer().HasSynced,
		r.csiInformer.Informer().HasSynced,
		r.nodeInformer.Informer().HasSynced,
		r.workerInformer.Informer().HasSynced,
		r.eventsInformer.Informer().HasSynced,
		r.controlEventsInformer.Informer().HasSynced) {
		return fmt.Errorf("could not sync caches for informers")
	}
	r.started = true
	slog.Info("Recorder is now considered STARTED after sync of informer caches", "recorderParams", r.params)
	context.AfterFunc(r.ctx, func() {
		_ = r.doClose()
	})
	return nil
}

func (r *defaultRecorder) runInformers(stopCh <-chan struct{}) {
	slog.Info("Calling informerFactory.Start()")
	slog.Info("Calling controllerInformerFactory.Start()")
	r.informerFactory.Start(stopCh)
	r.controlInformerFactory.Start(stopCh)
}

func (r *defaultRecorder) onAddMCD(obj interface{}) {
	mcd := obj.(*unstructured.Unstructured)
	if mcd.GetDeletionTimestamp() != nil {
		slog.Error("onAddMCD: MachineDeployment is already deleted.", "Name", mcd.GetName(), "DeletionTimestamp", mcd.GetDeletionTimestamp(), "recorderParams", r.params)
		return
	}
	err := r.processMCD(nil, mcd)
	if err != nil {
		slog.Error("onAddMCD failed.", "error", err, "recorderParams", r.params)
	}
}

func (r *defaultRecorder) onUpdateMCD(old any, new any) {
	oldObj := old.(*unstructured.Unstructured)
	newObj := new.(*unstructured.Unstructured)
	if newObj == nil {
		return
	}
	err := r.processMCD(oldObj, newObj)

	if err != nil {
		slog.Error("onUpdateMCD Failed.", "error", err)
	}
}

func (r *defaultRecorder) onAddMCC(obj interface{}) {
	mcc := obj.(*unstructured.Unstructured)
	if mcc.GetDeletionTimestamp() != nil {
		slog.Error("onAddMCC: MachineClass is already deleted.", "Name", mcc.GetName(), "DeletionTimestamp", mcc.GetDeletionTimestamp())
		return
	}
	err := r.processMCC(nil, mcc)
	if err != nil {
		slog.Error("onAddMCC failed.", "error", err)
	}
}

func (r *defaultRecorder) onUpdateMCC(old any, new any) {
	oldObj := old.(*unstructured.Unstructured)
	newObj := new.(*unstructured.Unstructured)
	if newObj == nil {
		return
	}
	err := r.processMCC(oldObj, newObj)

	if err != nil {
		slog.Error("onUpdateMCC Failed.", "error", err)
	}
}

func getLastUpdateTime(conditions []corev1.NodeCondition) (lastUpdate time.Time) {
	for _, condition := range conditions {
		if condition.LastTransitionTime.After(lastUpdate) {
			lastUpdate = condition.LastTransitionTime.Time
		}
	}
	lastUpdate = lastUpdate.UTC()
	return
}

func getNodeConditionsFromUnstructuredMCD(obj *unstructured.Unstructured) (conditions []corev1.NodeCondition) {
	parentMap := obj.UnstructuredContent()
	statusMap, err := GetInnerMap(parentMap, "status")
	if err != nil {
		slog.Error("cannot get status from mcd", "error", err)
		return
	}
	conditionsObj, ok := statusMap["conditions"].([]any)
	if !ok {
		slog.Warn("cannot find conditions for mcd", "mcd.name", obj.GetName())
		return
	}
	if conditionsObj == nil {
		slog.Warn("cannot find conditions for mcd", "mcd.name", obj.GetName())
		return
	}
	for _, conditionObj := range conditionsObj {
		var condition corev1.NodeCondition
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(conditionObj.(map[string]any), &condition)
		if err != nil {
			slog.Error("cannot get the nodecondition obj from unstructured mcd", "error", err)
		}
		slog.Info("NodeCondition for the mcd", "mcd.Name", obj.GetName(), "conditions", condition)
		conditions = append(conditions, condition)
	}
	return
}

func (r *defaultRecorder) onDeleteMCD(obj any) {
	if obj == nil {
		return
	}
	mcdObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			slog.Error("onDeleteMCD got an obj that is neither unstructured nor cache.DeletedFinalStateUnknown", "object", obj)
			return
		}
		mcdObj, ok = tombstone.Obj.(*unstructured.Unstructured)
		if !ok {
			slog.Error("Tombstone contained object that is not a unstructured", "object", obj)
			return
		}
	}
	delTimeStamp := time.Now().UTC() // shitty issue where sometimes >node.DeletionTimestamp is nil
	if mcdObj.GetDeletionTimestamp() != nil {
		delTimeStamp = mcdObj.GetDeletionTimestamp().UTC()
	}
	mcdName := mcdObj.GetName()
	slog.Info("onDeleteMCD: Updating the DeletionTimestamp for MachineDeploymentInfo with given name.", "Name", mcdName, "DeletionTimestamp", delTimeStamp)
	_, err := r.dataAccess.UpdateMCDInfoDeletionTimestamp(mcdName, delTimeStamp)
	if err != nil {
		slog.Error("cannot update the deletion timestamp for the MachineDeploymentInfo", "Name", mcdName, "DeletionTimestamp", delTimeStamp)
	}
}

func (r *defaultRecorder) onDeleteMCC(obj interface{}) {
	if obj == nil {
		return
	}
	mccObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			slog.Error("onDeleteMCC got an obj that is neither unstructured nor cache.DeletedFinalStateUnknown", "object", obj)
			return
		}
		mccObj, ok = tombstone.Obj.(*unstructured.Unstructured)
		if !ok {
			slog.Error("Tombstone contained object that is not a unstructured", "object", obj)
			return
		}
	}
	delTimeStamp := time.Now().UTC() // shitty issue where sometimes >node.DeletionTimestamp is nil
	if mccObj.GetDeletionTimestamp() != nil {
		delTimeStamp = mccObj.GetDeletionTimestamp().UTC()
	}
	mccName := mccObj.GetName()
	slog.Info("onDeleteMCC: Updating the DeletionTimestamp for MachineClassInfo with given name.", "Name", mccName, "DeletionTimestamp", delTimeStamp)
	_, err := r.dataAccess.UpdateMCCInfoDeletionTimestamp(mccName, delTimeStamp)
	if err != nil {
		slog.Error("cannot update the deletion timestamp for the MachineClassInfo", "Name", mccName, "DeletionTimestamp", delTimeStamp)
	}
}

func parseCASettingsInfo(caDeploymentData map[string]any) (caSettings gsc.CASettingsInfo, err error) {
	caSettings.NodeGroupsMinMax = make(map[string]gsc.MinMax)
	containersVal, err := gsc.GetInnerMapValue(caDeploymentData, "spec", "template", "spec", "containers")
	if err != nil {
		return
	}
	containers := containersVal.([]any)
	if len(containers) == 0 {
		err = fmt.Errorf("len of containers is zero, no CA container found")
		return
	}
	caContainer := containers[0].(map[string]any)
	caCommands := caContainer["command"].([]any)
	for _, commandVal := range caCommands {
		command := commandVal.(string)
		vals := strings.Split(command, "=")
		if len(vals) <= 1 {
			continue
		}
		key := vals[0]
		val := vals[1]
		switch key {
		case "--max-graceful-termination-sec":
			caSettings.MaxGracefulTerminationSeconds, err = strconv.Atoi(val)
		case "--max-node-provision-time":
			caSettings.MaxNodeProvisionTime, err = time.ParseDuration(val)
		case "--scan-interval":
			if val == "10s" {
				caSettings.ScanInterval = 10 * time.Second // because otherwise some oauth2.defaultExpiryDelta constant is taken
			} else {
				caSettings.ScanInterval, err = time.ParseDuration(val)
			}
		case "--max-empty-bulk-delete":
			caSettings.MaxEmptyBulkDelete, err = strconv.Atoi(val)
		case "--new-pod-scale-up-delay":
			caSettings.NewPodScaleUpDelay, err = time.ParseDuration(val)
		case "--ignore-daemonsets-utilization":
			caSettings.IgnoreDaemonSetUtilization, err = strconv.ParseBool(val)
		case "--max-nodes-total":
			caSettings.MaxNodesTotal, err = strconv.Atoi(val)
		case "--nodes":
			var ngMinMax gsc.MinMax
			ngVals := strings.Split(val, ":")
			ngMinMax.Min, err = strconv.Atoi(ngVals[0])
			ngMinMax.Max, err = strconv.Atoi(ngVals[1])
			caSettings.NodeGroupsMinMax[ngVals[2]] = ngMinMax
		case "--expander":
			caSettings.Expander = val
		}
		if err != nil {
			return
		}
	}
	return
}

func (r *defaultRecorder) onAddDeployment(obj interface{}) {
	if obj == nil {
		return
	}
	deployment, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	if deployment.GetName() != "cluster-autoscaler" {
		return
	}
	caSettings, err := parseCASettingsInfo(deployment.UnstructuredContent())
	if err != nil {
		slog.Error("onAddDeployment cannot parse the ca command from deployment", "error", err)
		return
	}
	caSettings.SnapshotTimestamp = time.Now().UTC()
	storedCASettingsInfo, err := r.dataAccess.LoadLatestCASettingsInfo()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("onAddDeployment cannot get the latest ca settings stored in db", "error", err)
			return
		}
	}
	caSettings.Priorities = storedCASettingsInfo.Priorities
	caSettings.Hash = caSettings.GetHash()
	if storedCASettingsInfo.Hash != caSettings.Hash {
		_, err := r.dataAccess.StoreCASettingsInfo(caSettings)
		if err != nil {
			slog.Error("onAddDeployment cannot store ca settings in ca_settings_info", "error", err)
			return
		}
	}
	return

}

func (r *defaultRecorder) onUpdateDeployment(_, newObj interface{}) {
	r.onAddDeployment(newObj)
}

func getPrirotiesFromCAConfig(obj *corev1.ConfigMap) string {
	return obj.Data["priorities"]
}

func (r *defaultRecorder) onAddConfigMap(obj any) {
	if obj == nil {
		return
	}
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		slog.Error("onAddConfigMap not invoked with corev1.configMap", "obj", obj, "recorderParams", r.params)
	}
	if configMap.GetName() != "cluster-autoscaler-priority-expander" {
		return
	}
	priorities := getPrirotiesFromCAConfig(configMap)
	if priorities == "" {
		slog.Warn("onAddConfigMap found no priorities defined in configmap", "configMap", configMap, "recorderParams", r.params)
	} else {
		slog.Debug("Found priorities defined in configmap", "configMap", configMap, "priorities", priorities, "recorderParams", r.params)
	}

	caSettings, err := r.dataAccess.LoadLatestCASettingsInfo()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("onAddConfigMap cannot get the latest ca deployment stored in db", "error", err, "recorderParams", r.params)
			return
		}
	}

	caSettings.Priorities = priorities
	oldHash := caSettings.Hash
	caSettings.Hash = caSettings.GetHash()
	if caSettings.Hash != oldHash {
		_, err = r.dataAccess.StoreCASettingsInfo(caSettings)
		if err != nil {
			slog.Error("onAddConfigMap cannot store ca settings in ca_settings_info", "error", err, "recorderParams", r.params)
			return
		}
	}
	return
}

func (r *defaultRecorder) onUpdateConfigMap(_, newObj interface{}) {
	r.onAddConfigMap(newObj)
}

func getEventTimeFromUnstructured(event *unstructured.Unstructured) (eventTime time.Time, err error) {
	firstTimestamp, ftok := event.UnstructuredContent()["firstTimestamp"].(*time.Time)
	lastTimestamp, ltok := event.UnstructuredContent()["lastTimestamp"].(*time.Time)
	creationTimestamp := event.GetCreationTimestamp()
	if !(&creationTimestamp).IsZero() {
		eventTime = event.GetCreationTimestamp().UTC()
	} else if ftok && !firstTimestamp.IsZero() {
		eventTime = firstTimestamp.UTC()
	} else if ltok && !lastTimestamp.IsZero() {
		eventTime = lastTimestamp.UTC()
	} else {
		slog.Warn("event has zero timestamp.", "event.UID", event.GetUID(), "event.Name", event.GetName())
		err = fmt.Errorf("cannot get event time for event %s", event.GetName())
	}
	return
}

func (r *defaultRecorder) onAddControlEvent(obj interface{}) {
	eventObj := obj.(*unstructured.Unstructured)
	parentMap := eventObj.UnstructuredContent()
	eventName := eventObj.GetName()
	sourceComponent, err := GetInnerMapValue(eventObj.UnstructuredContent(), "source", "component")
	if errors.Is(err, ErrKeyNotFound) {
		sourceComponent, ok := eventObj.UnstructuredContent()["reportingComponent"]
		if !ok {
			slog.Warn("cannot find neither source nor reporting component for event", "event.name", eventName)
			return
		}
		if sourceComponent.(string) != "machine-controller-manager" {
			return
		}
	}
	if sourceComponent != "machine-controller-manager" || err != nil {
		return
	}
	eventKind, err := GetInnerMapValue(eventObj.UnstructuredContent(), "involvedObject", "kind")
	if err != nil {
		slog.Error("cannot get the event kind", "event.name", eventName)
	}

	reason := parentMap["reason"].(string)
	involvedObjectName, err := GetInnerMapValue(eventObj.UnstructuredContent(), "involvedObject", "name")
	if err != nil {
		slog.Error("cannot get the involvedObject name", "event.name", eventName)
	}
	involvedObjectNamespace, err := GetInnerMapValue(eventObj.UnstructuredContent(), "involvedObject", "name")
	if err != nil {
		slog.Error("cannot get the involvedObject namespace", "event.name", eventName)
	}
	involvedObjectUID, err := GetInnerMapValue(eventObj.UnstructuredContent(), "involvedObject", "uid")
	if err != nil {
		slog.Error("cannot get the involvedObject uid", "event.name", eventName)
	}

	eventTime, err := getEventTimeFromUnstructured(eventObj)
	if eventTime.Before(r.startTime) {
		return
	}
	message := parentMap["message"].(string)
	event := gsc.EventInfo{
		UID:                     string(eventObj.GetUID()),
		EventTime:               eventTime,
		ReportingController:     sourceComponent.(string),
		Reason:                  reason,
		Message:                 message,
		InvolvedObjectKind:      eventKind.(string),
		InvolvedObjectName:      involvedObjectName.(string),
		InvolvedObjectNamespace: involvedObjectNamespace.(string),
		InvolvedObjectUID:       involvedObjectUID.(string),
	}
	err = r.dataAccess.StoreEventInfo(event)
	if err != nil {
		slog.Error("cannot store the event in event_info", "error", err, "event", eventName)
		return
	}
	if eventKind.(string) != "MachineDeployment" {
		return
	}
	if reason != "ScalingMachineSet" {
		return
	}
	//isScaledUp := strings.HasPrefix(message, "Scaled up")
	//if isScaledUp {
	//	targetSize, err := parseMachineSetScaleUpMessage(message)
	//	if err != nil {
	//		slog.Error("cannot parse the machine set target size for event", "error", err, "event.name", eventName, "message", message)
	//	}
	//	ngName := involvedObjectName.(string)
	//	if err != nil {
	//		return
	//	}
	//	err = r.StoreLatestNodeGroupCurrentSizeAndTargetSize(ngName, eventTime, targetSize)
	//	if err != nil {
	//		return
	//	}
	//	slog.Info("successfully insert the nodegroup with target size", "nodegroup.name", ngName, "targetSize", targetSize)
	//}
}

func (r *defaultRecorder) onAddCSINode(obj interface{}) {
	csiNode := obj.(*storagev1.CSINode)
	allocatableCount := -1
	if len(csiNode.Spec.Drivers) != 0 {
		allocatableResources := csiNode.Spec.Drivers[0].Allocatable
		if allocatableResources != nil {
			allocatableCount = int(*allocatableResources.Count)
		}
	}
	//r.nodeAllocatableVolumes.Store(csiNode.Name, allocatableCount)
	_, err := r.dataAccess.StoreCSINodeRow(db.CSINodeRow{
		Name:               csiNode.Name,
		Namespace:          csiNode.Namespace,
		CreationTimestamp:  csiNode.CreationTimestamp.UTC().UnixMicro(),
		SnapshotTimestamp:  time.Now().UTC().UnixMicro(),
		AllocatableVolumes: allocatableCount,
	})
	if err != nil {
		slog.Error("could not store csiNodeRow.", "Name", csiNode.Name, "error", err, "recorderParams", r.params)
	}
	// update the node info.allocablev volumes  table for the csi node id.
}

func (r *defaultRecorder) onUpdateCSINode(_ interface{}, newObj interface{}) {
	r.onAddCSINode(newObj)
}

func (r *defaultRecorder) onDeleteCSINode(obj any) {
	if obj == nil {
		return
	}
	csiNode, ok := obj.(*storagev1.CSINode)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			slog.Error("onDeleteCSINode got an obj that is neither csinode nor cache.DeletedFinalStateUnknown", "object", obj)
			return
		}
		csiNode, ok = tombstone.Obj.(*storagev1.CSINode)
		if !ok {
			slog.Error("Tombstone contained object that is not a CSINode", "object", obj)
			return
		}
	}
	delTimeStamp := time.Now().UTC() // shitty issue where sometimes >node.DeletionTimestamp is nil
	if csiNode.DeletionTimestamp != nil {
		delTimeStamp = csiNode.DeletionTimestamp.UTC()
	}
	rowsUpdated, err := r.dataAccess.UpdateCSINodeRowDeletionTimestamp(csiNode.Name, delTimeStamp)
	slog.Debug("updated DeletionTimestamp of csiNode.", "Name", csiNode.Name, "csiNode.DeletionTimestamp", delTimeStamp, "rows.updated", rowsUpdated)
	if err != nil {
		slog.Error("could not execute UpdateCSINodeInfoDeletionTimestamp ", "error", err, "Name", csiNode.Name, "csiNode.DeletionTimestamp", delTimeStamp, "recorderParams", r.params)
	}
	//r.nodeAllocatableVolumes.Delete(csiNode.Name)
}

func (r *defaultRecorder) processMCD(mcdOld, mcdNew *unstructured.Unstructured) error {
	var err error
	var mcdOldInfo, mcdNewInfo gsc.MachineDeploymentInfo
	var mcdOldHash string
	var mcdName = mcdNew.GetName()
	now := time.Now().UTC()
	if mcdOld != nil {
		mcdOldInfo, err = gsh.MachineDeploymentInfoFromUnstructured(mcdOld, now)
		if err != nil {
			return err
		}
		mcdOldHash = mcdOldInfo.Hash
	}
	if mcdOldHash == "" {
		mcdOldHash, err = r.dataAccess.GetMachineDeploymentInfoHash(mcdName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("error looking up MachineDeploymentInfo hash for name %q: %w", mcdName, err)
		}
	}
	mcdNewInfo, err = gsh.MachineDeploymentInfoFromUnstructured(mcdNew, now)
	if err != nil {
		return err
	}
	if mcdOldHash != mcdNewInfo.Hash {
		_, err = r.dataAccess.StoreMachineDeploymentInfo(mcdNewInfo)
	} else {
		slog.Debug("skipping store of MachineDeploymentInfo", "Name", mcdName, "Hash", mcdNewInfo.Hash)
	}
	return err
}

func (r *defaultRecorder) processMCC(mccOld, mccNew *unstructured.Unstructured) error {
	var err error
	var mccOldInfo, mccNewInfo gsh.MachineClassInfo
	var mccOldHash string
	var mccName = mccNew.GetName()
	now := time.Now().UTC()
	if mccOld != nil {
		mccOldInfo, err = gsh.MachineClassInfoFromUnstructured(mccOld, now)
		if err != nil {
			return err
		}
		mccOldHash = mccOldInfo.Hash
	}
	if mccOldHash == "" {
		mccOldHash, err = r.dataAccess.GetMachineClassInfoHash(mccName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("error looking up MachineClassInfo hash for name %q: %w", mccName, err)
		}
	}
	mccNewInfo, err = gsh.MachineClassInfoFromUnstructured(mccNew, now)
	if err != nil {
		return err
	}
	if mccOldHash != mccNewInfo.Hash {
		_, err = r.dataAccess.StoreMachineClassInfo(mccNewInfo)
	} else {
		slog.Debug("skipping store of MachineClassInfo", "Name", mccName, "Hash", mccNewInfo.Hash)
	}
	return err
}

func getLastUpdateTimeForPod(p *corev1.Pod) (lastUpdateTime time.Time) {
	lastUpdateTime = p.ObjectMeta.CreationTimestamp.UTC()
	for _, condition := range p.Status.Conditions {
		lastTransitionTime := condition.LastTransitionTime.UTC()
		if lastTransitionTime.After(lastUpdateTime) {
			lastUpdateTime = lastTransitionTime
		}
	}
	return
}

func pcInfoFromPC(p *schedulingv1.PriorityClass) gsc.PriorityClassInfo {
	pc := gsc.PriorityClassInfo{
		SnapshotTimestamp: time.Now().UTC(),
		PriorityClass:     *p,
	}
	pc.Hash = pc.GetHash()
	return pc
}

func PodInfoFromPod(p *corev1.Pod) gsc.PodInfo {
	var pi gsc.PodInfo
	pi.UID = string(p.UID)
	pi.Name = p.Name
	pi.Namespace = p.Namespace
	pi.CreationTimestamp = p.CreationTimestamp.UTC()
	pi.SnapshotTimestamp = getLastUpdateTimeForPod(p)
	pi.NodeName = p.Spec.NodeName
	pi.Labels = p.Labels
	pi.Requests = gsc.CumulatePodRequests(p)
	pi.Spec = p.Spec
	pi.PodPhase = p.Status.Phase
	pi.PodScheduleStatus = ComputePodScheduleStatus(p)
	pi.Hash = pi.GetHash()
	return pi
}

// ComputePodScheduleStatus -1 => NotDetermined, 0 => Scheduled, 1 => Unscheduled
func ComputePodScheduleStatus(pod *corev1.Pod) (scheduleStatus gsc.PodScheduleStatus) {

	scheduleStatus = gsc.PodSchedulePending

	if len(pod.Status.Conditions) == 0 && pod.Status.Phase == corev1.PodPending {
		return scheduleStatus
	}

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Reason == corev1.PodReasonUnschedulable {
			//Creation Time does not change for a unschedulable pod with single unschedulable condition.
			scheduleStatus = gsc.PodUnscheduled
			break
		}
	}

	if pod.Spec.NodeName != "" {
		scheduleStatus = gsc.PodScheduleCommited
		return
	}

	if pod.Status.NominatedNodeName != "" {
		scheduleStatus = gsc.PodScheduleNominated
		return
	}

	if IsOwnedBy(pod, []schema.GroupVersionKind{
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Version: "v1", Kind: "Node"},
	}) {
		scheduleStatus = gsc.PodScheduleCommited
	}

	return scheduleStatus
}

func CreateRecorderParams(ctx context.Context, mode gsh.ExecutionMode, configDir string, dbDir string) ([]gsh.RecorderParams, error) {
	clusterConfigPath := path.Join(configDir, CLUSTERS_CFG_FILE)

	landscapeKubeconfigs, err := apputil.GetLandscapeKubeconfigs(mode)

	if err != nil {
		return nil, err
	}

	result, err := os.ReadFile(clusterConfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot load clusters config %q %w", clusterConfigPath, err)
	}
	reader := csv.NewReader(strings.NewReader(string(result)))
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("cannot parse clusters config: %w", err)
	}
	var recorderParams = make([]gsh.RecorderParams, len(records))
	for rowIndex, row := range records {
		if len(row) != 3 {
			return nil, fmt.Errorf("invalid row in cluster config %q, should be 3 colums", clusterConfigPath)
		}
		landscapeName := strings.TrimSpace(row[0])
		projectName := strings.TrimSpace(row[1])
		shootName := strings.TrimSpace(row[2])

		slog.Info("Creating landscape client.", "landscapeName", landscapeName, "projectName", projectName, "shootName", shootName)
		landscapeKubeconfig, ok := landscapeKubeconfigs[landscapeName]
		if !ok {
			return nil, fmt.Errorf("cannot find kubeconfig for landscape %q", landscapeName)
		}
		landscapeClient, err := apputil.CreateLandscapeClient(landscapeKubeconfig, mode)
		if err != nil {
			return nil, fmt.Errorf("cannot create landscape client for landscape %q, projectName %q, shootName %q: %w", landscapeName, projectName, shootName, err)
		}

		seedName, err := apputil.GetSeedName(ctx, landscapeClient, projectName, shootName)
		if err != nil {
			return nil, fmt.Errorf("cannot get seed for landscape %q projectName %q shootName %q: %w", landscapeName, projectName, shootName, err)
		}
		slog.Info("Obtained seedName for", "seedName", seedName, "landscapeName", landscapeName, "projectName", projectName, "shootName", shootName)

		shootNamespace := fmt.Sprintf("shoot--%s--%s", projectName, shootName)

		shootKubeconfigPath, err := apputil.GetViewerKubeconfig(ctx, landscapeClient, landscapeName, projectName, shootName)
		if err != nil {
			return nil, fmt.Errorf("cannot get viewer kubeconfig for shoot %q: %w", shootName, err)
		}

		seedKubeconfigPath, err := apputil.GetViewerKubeconfig(ctx, landscapeClient, landscapeName, "garden", seedName)
		if err != nil {
			return nil, fmt.Errorf("cannot get viewer kubeconfig for seed %q: %w", seedName, err)
		}

		rp := gsh.RecorderParams{
			Mode:                mode,
			Landscape:           landscapeName,
			ProjectName:         projectName,
			ShootName:           shootName,
			SeedName:            seedName,
			ShootNameSpace:      shootNamespace,
			ShootKubeConfigPath: shootKubeconfigPath,
			SeedKubeConfigPath:  seedKubeconfigPath,
			DBDir:               dbDir,
		}

		slog.Info("Created recorder params", "mode", mode, "landscape", landscapeName, "shootNamespace", shootNamespace, "shootKubeConfigPath", shootKubeconfigPath, "seedKubeConfigPath", seedKubeconfigPath, "dbDir", dbDir)
		recorderParams[rowIndex] = rp
	}
	return recorderParams, nil
}
