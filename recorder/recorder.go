package recorder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scalehist"
	"github.com/elankath/gardener-scalehist/db"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	policyv1informers "k8s.io/client-go/informers/policy/v1"
	storagev1informers "k8s.io/client-go/informers/storage/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var machineDeploymentGVR = schema.GroupVersionResource{Group: "machine.sapcloud.io", Version: "v1alpha1", Resource: "machinedeployments"}
var workerGVR = schema.GroupVersionResource{Group: "extensions.gardener.cloud", Version: "v1alpha1", Resource: "workers"}
var deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
var configmapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
var eventGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

var ErrKeyNotFound = errors.New("key not found")

const podTriggerScaleUpPattern = `.*(shoot--\S+) (\d+)\->(\d+) .*max: (\d+).*`
const machineSetScaleUpPattern = `Scaled up.*? to (\d+)`

var podTriggeredScaleUpRegex = regexp.MustCompile(podTriggerScaleUpPattern)
var machineSetScaleUpRegex = regexp.MustCompile(machineSetScaleUpPattern)

func NewDefaultRecorder(params scalehist.RecorderParams, startTime time.Time) (scalehist.Recorder, error) {
	// Load kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", params.ShootKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	if params.SchedulerName == "" {
		params.SchedulerName = "bin-packing-scheduler"
		slog.Info("scheduler name un-specified. defaulting", "SchedulerName", params.SchedulerName)
	}

	controlConfig, err := clientcmd.BuildConfigFromFlags("", params.SeedKubeConfigPath)
	if err != nil {
		slog.Error("cannot create the client config for the control plane", "error", err)
		return nil, fmt.Errorf("cannot create the client config for the control plane: %w", err)
	}
	controlClientSet, err := dynamic.NewForConfig(controlConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset for control plane: %w", err)
	}

	connChecker, err := NewConnChecker(config, controlConfig)
	if err != nil {
		slog.Error("cannot create the conn checker", "error", err)
		return nil, err
	}

	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	slog.Info("Building recorder", "recorder-params", params)
	controlInformerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(controlClientSet, 0, params.ShootNameSpace, nil)
	dataDBName := strings.TrimSuffix(strings.TrimPrefix(path.Base(params.ShootKubeConfigPath), "kubeconfig-"), ".yaml") + ".db"
	dataDBPath := path.Join(params.DBDir, dataDBName)
	slog.Info("data db path.", "dataDBPath", dataDBPath)
	return &defaultRecorder{params: &params,
		startTime:              startTime,
		connChecker:            connChecker,
		informerFactory:        informerFactory,
		eventsInformer:         informerFactory.Core().V1().Events(),
		controlEventsInformer:  controlInformerFactory.ForResource(eventGVR),
		podsInformer:           informerFactory.Core().V1().Pods(),
		pdbInformer:            informerFactory.Policy().V1().PodDisruptionBudgets(),
		nodeInformer:           informerFactory.Core().V1().Nodes(),
		csiInformer:            informerFactory.Storage().V1().CSINodes(),
		controlInformerFactory: controlInformerFactory,
		mcdInformer:            controlInformerFactory.ForResource(machineDeploymentGVR),
		deploymentInformer:     controlInformerFactory.ForResource(deploymentGVR),
		configmapInformer:      controlInformerFactory.ForResource(configmapGVR),
		workerInformer:         controlInformerFactory.ForResource(workerGVR),
		dataAccess:             db.NewDataAccess(dataDBPath),
	}, nil
}

var _ scalehist.Recorder = (*defaultRecorder)(nil)

type defaultRecorder struct {
	params                 *scalehist.RecorderParams
	startTime              time.Time
	connChecker            *ConnChecker
	informerFactory        informers.SharedInformerFactory
	eventsInformer         corev1informers.EventInformer
	controlEventsInformer  informers.GenericInformer
	podsInformer           corev1informers.PodInformer
	pdbInformer            policyv1informers.PodDisruptionBudgetInformer
	nodeInformer           corev1informers.NodeInformer
	csiInformer            storagev1informers.CSINodeInformer
	controlInformerFactory dynamicinformer.DynamicSharedInformerFactory
	mcdInformer            informers.GenericInformer
	workerInformer         informers.GenericInformer
	deploymentInformer     informers.GenericInformer
	configmapInformer      informers.GenericInformer
	dataAccess             *db.DataAccess
	workerPoolsMap         sync.Map
	mcdMinMaxMap           sync.Map
	mcdHashesMap           sync.Map
	nodeAllocatableVolumes sync.Map
	stopCh                 <-chan struct{}
}

type sizeLimits struct {
	Name string
	Min  int
	Max  int
}

func (m sizeLimits) String() string {
	return fmt.Sprintf("MinMaxSize(Name:%s,Min:%d,Max:%d)", m.Name, m.Min, m.Max)
}

func (r *defaultRecorder) Close() error {
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

func parseWorkerPools(worker *unstructured.Unstructured) (map[string]scalehist.WorkerPool, error) {
	specMapObj, err := GetInnerMap(worker.UnstructuredContent(), "spec")
	if err != nil {
		slog.Error("error getting the inner map for worker", "error", err)
		return nil, err
	}
	workerPools := make(map[string]scalehist.WorkerPool)
	poolsList := specMapObj["pools"].([]any)
	for _, pool := range poolsList {
		poolMap := pool.(map[string]any)
		var zones []string
		for _, z := range poolMap["zones"].([]any) {
			zones = append(zones, z.(string))
		}

		maxSurge, err := parseIntOrStr(poolMap["maxSurge"])
		if err != nil {
			//TODO make better error message
			return nil, err
		}
		maxUnavailable, err := parseIntOrStr(poolMap["maxUnavailable"])
		if err != nil {
			//TODO make better error message
			return nil, err
		}
		wp := scalehist.WorkerPool{
			Name:            poolMap["name"].(string),
			Minimum:         int(poolMap["minimum"].(int64)),
			Maximum:         int(poolMap["maximum"].(int64)),
			MaxSurge:        maxSurge,
			MaxUnavailable:  maxUnavailable,
			ShootGeneration: worker.GetGeneration(),
			MachineType:     poolMap["machineType"].(string),
			Architecture:    poolMap["architecture"].(string),
			Zones:           zones,
		}
		workerPools[wp.Name] = wp
	}
	return workerPools, nil
}

func (r *defaultRecorder) onAddPod(obj any) {
	r.onUpdatePod(nil, obj)
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

// ComputePodScheduleStatus -1 => NotDetermined, 0 => Scheduled, 1 => Unscheduled
func ComputePodScheduleStatus(pod *corev1.Pod) (scheduleStatus scalehist.PodScheduleStatus) {

	scheduleStatus = scalehist.PodSchedulePending

	if len(pod.Status.Conditions) == 0 && pod.Status.Phase == corev1.PodPending {
		return scheduleStatus
	}

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Reason == corev1.PodReasonUnschedulable {
			//Creation Time does not change for a unschedulable pod with single unschedulable condition.
			scheduleStatus = scalehist.PodUnscheduled
			break
		}
	}

	if pod.Spec.NodeName != "" {
		scheduleStatus = scalehist.PodScheduleCommited
		return
	}

	if pod.Status.NominatedNodeName != "" {
		scheduleStatus = scalehist.PodScheduleNominated
		return
	}

	if IsOwnedBy(pod, []schema.GroupVersionKind{
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Version: "v1", Kind: "Node"},
	}) {
		scheduleStatus = scalehist.PodScheduleCommited
	}

	return scheduleStatus
}

func (r *defaultRecorder) onUpdatePod(old, new any) {
	var podOld *corev1.Pod
	if old != nil {
		podOld = old.(*corev1.Pod)
	}
	podNew := new.(*corev1.Pod)
	if podNew.DeletionTimestamp != nil {
		// ignore deletes and pod with no node
		return
	}
	if podOld != nil {
		slog.Debug("Pod Updated.", "podName", podOld.Name, "podOld.UID", podOld.UID,
			"podNew.UID", podNew.UID)
	} else {
		slog.Info("Pod Created.", "podName", podNew.Name,
			"podNew.UID", podNew.UID)
	}

	podInfo := podInfoFromPod(podNew)
	if podInfo.PodScheduleStatus == scalehist.PodSchedulePending {
		slog.Info("pod is in PodSchedulePending state, skipping persisting it", "pod.UID", podInfo.UID, "pod.Name", podInfo.Name)
		return
	}

	podCountWithSpecHash, err := r.dataAccess.CountPodInfoWithSpecHash(string(podNew.UID), podInfo.Hash)
	if err != nil {
		slog.Error("CountPodInfoWithSpecHash failed", "error", err, "pod.Name", podNew.Name, "pod.uid", podNew.UID, "pod.hash", podInfo.Hash)
		return
	}

	if podCountWithSpecHash > 0 {
		slog.Debug("pod is already inserted with hash", "pod.Name", podNew.Name, "pod.uid", podNew.UID, "pod.nodeName", podNew.Spec.NodeName, "pod.Hash", podInfo.Hash)
		return
	}

	_, err = r.dataAccess.StorePodInfo(podInfo)
	if err != nil {
		slog.Error("could not execute pod_info insert", "error", err, "pod.Name", podInfo.Name, "pod.UID", podInfo.UID, "pod.CreationTimestamp", podInfo.CreationTimestamp, "pod.Hash", podInfo.Hash)
		return
	}
}

func (r *defaultRecorder) onDeletePod(obj any) {
	pod := obj.(*corev1.Pod)
	if pod.DeletionTimestamp == nil {
		return //sometimes this handler is invoked with null deletiontimestamp!
	}
	rowsUpdated, err := r.dataAccess.UpdatePodDeletionTimestamp(pod.UID, pod.DeletionTimestamp.Time.UTC())
	slog.Info("updated deletionTimestamp of pod", "pod.name", pod.Name, "pod.uid", pod.UID, "pod.deletionTimestamp", pod.DeletionTimestamp, "rows.updated", rowsUpdated)
	if err != nil {
		slog.Error("could not execute pod deletion timestamp update", "error", err)
	}
}

func (r *defaultRecorder) onAddNode(obj interface{}) {
	r.onUpdateNode(nil, obj)
}

func (r *defaultRecorder) onUpdateNode(old interface{}, new interface{}) {
	nodeNew := new.(*corev1.Node)
	if nodeNew.DeletionTimestamp != nil {
		// ignore deletes
		return
	}
	slog.Debug("onUpdateNode invoked.", "nodeNew.name", nodeNew.Name)
	var nodeOld *corev1.Node
	if nodeOld != nil {
		nodeOld = old.(*corev1.Node)
	}
	nodeNewInfo := r.nodeInfoFromNode(nodeNew)
	InvokeOrScheduleFunc("onUpdateNode", 10*time.Second, nodeNewInfo, func(_ scalehist.NodeInfo) error {
		nodeNewInfo := r.nodeInfoFromNode(nodeNew)
		if nodeNewInfo.AllocatableVolumes == 0 {
			slog.Warn("Allocatable Volumes key not found. Skipping insert", "node.name", nodeNewInfo.Name)
			return ErrKeyNotFound
		}
		countWithSameHash, err := r.dataAccess.CountNodeInfoWithHash(nodeNew.Name, nodeNewInfo.Hash)
		if err != nil {
			slog.Error("cannot CountPodInfoWithSpecHash", "node.Name", nodeNew.Name, "node.Hash", nodeNewInfo.Hash, "error", err)
			return err
		}

		if countWithSameHash > 0 {
			slog.Debug("NodeInfo is already present with same hash", "node.Name", nodeNew.Name, "node.Hash", nodeNewInfo.Hash, "count", countWithSameHash, "error", err)
			return err
		}
		_, err = r.dataAccess.StoreNodeInfo(nodeNewInfo)
		if err != nil {
			return nil
		}
		return nil
	})
}

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

func (r *defaultRecorder) onDeleteNode(obj interface{}) {
	node := obj.(*corev1.Node)
	if node.DeletionTimestamp == nil {
		return
	}
	rowsUpdated, err := r.dataAccess.UpdateNodeInfoDeletionTimestamp(node.Name, node.DeletionTimestamp.UTC())
	slog.Info("updated DeletionTimestamp of Node.", "node.Name", node.Name, "node.DeletionTimestamp", node.DeletionTimestamp.UTC(), "rows.updated", rowsUpdated)
	if err != nil {
		slog.Error("could not execute UpdateNodeInfoDeletionTimestamp ", "error", err, "node.Name", node.Name, "node.DeletionTimestamp", node.DeletionTimestamp.UTC())
	}
}

// OnAdd(obj interface{}, isInInitialList bool)
func (r *defaultRecorder) onAddEvent(obj any) {
	event := obj.(*corev1.Event)
	isCAEvent := event.Source.Component == "cluster-autoscaler" || event.ReportingController == "cluster-autoscaler"
	isSchedulerEvent := strings.Contains(event.Source.Component, "scheduler") ||
		strings.Contains(event.ReportingController, "scheduler")
	isTriggerScaleUp := strings.Contains(event.Reason, "TriggeredScaleUp")
	//isScaledUpNodeGroupEvent := strings.Contains(event.Reason, "ScaledUpGroup")
	isNodeControllerEvent := strings.Contains(event.ReportingController, "node-controller")
	if isTriggerScaleUp {
		slog.Info("TriggeredScaleUp.", "event.Message", event.Message, "event.CreationTimestamp", event.CreationTimestamp)
	}
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
	var reportingController string
	if event.ReportingController != "" {
		reportingController = event.ReportingController
	} else {
		reportingController = event.Source.Component
	}

	eventInfo := scalehist.EventInfo{
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
			slog.Error("could not execute event insert", "error", err)
			errCount++
		}
	}
	if isTriggerScaleUp {
		InvokeOrScheduleFunc("storeAssociateNodeGroupWithScaleUp", 10*time.Second, eventInfo, r.storeAssociateNodeGroupWithScaleUp)
		InvokeOrScheduleFunc("storeAssociateNodeGroupWithScaleUp", 10*time.Second, eventInfo, r.storeAssociatedCASettingsInfo)
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

// pod triggered scale-up: [{shoot--i034796--aw2-p2-z1 1->3 (max: 3)}]
// fixme: only return CS,TS, MX as tuple.
func parseTriggeredScaleUpMessage(msg string) (ng scalehist.NodeGroupInfo, err error) {
	groups := podTriggeredScaleUpRegex.FindStringSubmatch(msg)
	ng.Name = groups[1]
	ng.CurrentSize, err = strconv.Atoi(groups[2])
	if err != nil { //TODO wrap in better error message
		return
	}
	ng.TargetSize, err = strconv.Atoi(groups[3])
	if err != nil {
		return
	}
	ng.MaxSize, err = strconv.Atoi(groups[4])
	if err != nil {
		return
	}
	return
}

func (r *defaultRecorder) storeAssociatedCASettingsInfo(eventInfo scalehist.EventInfo) error {
	caSettings, err := r.dataAccess.GetLatestCADeployment()
	if err != nil {
		return err
	}
	assoc := scalehist.EventCASettingsAssoc{
		EventUID:       eventInfo.UID,
		CASettingsHash: caSettings.Hash,
	}
	slog.Info("storeAssociatedCASettingsInfo. Storing into event_ca_assoc.", "assoc", assoc)
	err = r.dataAccess.StoreEventCASettingsAssoc(assoc)
	if err != nil {
		slog.Error("cannot storeAssociatedCASettingsInfo", "error", err, "event.UID", eventInfo.UID, "event.Message", eventInfo.Message, "event.EventTime", eventInfo.EventTime)
	}
	return err
}
func (r *defaultRecorder) storeAssociateNodeGroupWithScaleUp(eventInfo scalehist.EventInfo) error {
	newNG, err := parseTriggeredScaleUpMessage(eventInfo.Message)
	if err != nil {
		return err //TODO: wrap with label
	}
	currNG, err := r.dataAccess.LoadLatestNodeGroup(newNG.Name)
	if err != nil {
		return fmt.Errorf("cannot find latest persisted node group with name %q and hence cannot persist new scale-up change in nodegroup_info: %w", newNG.Name, ErrKeyNotFound)
	}
	newNG.CreationTimestamp = eventInfo.EventTime
	newNG.MinSize = currNG.MinSize
	newNG.MachineType = currNG.MachineType
	newNG.Zone = currNG.Zone
	newNG.Architecture = currNG.Architecture
	newNG.PoolName = currNG.PoolName
	newNG.PoolMin = currNG.PoolMin
	newNG.PoolMax = currNG.PoolMax
	newNG.ShootGeneration = currNG.ShootGeneration
	newNG.MCDGeneration = currNG.MCDGeneration

	newNG.Hash = newNG.GetHash()
	if currNG.Hash == newNG.Hash {
		slog.Info("storeAssociateNodeGroupWithScaleUp: no change in nodegroup hash. skipping store for node group.", "newNG.name", newNG.Name, "event.Message", eventInfo.Message)
		newNG.RowID = currNG.RowID
	} else {
		slog.Info("storeAssociateNodeGroupWithScaleUp: currNG hash differs from newNG hash. Storing.", "currNG.hash", currNG.Hash, "newNG.hash", newNG.Hash)
		newNG.RowID, err = r.dataAccess.StoreNodeGroup(newNG)
	}

	assoc := scalehist.EventNodeGroupAssoc{
		EventUID:       eventInfo.UID,
		NodeGroupRowID: newNG.RowID,
		NodeGroupHash:  newNG.Hash,
	}
	slog.Info("storeAssociateNodeGroupWithScaleUp. Storing into event_nodegroup_assoc.", "assoc", assoc)
	err = r.dataAccess.StoreEventNGAssoc(assoc)
	if err != nil {
		slog.Warn("cannot storeAssociateNodeGroupWithScaleUp", "event.uid", eventInfo.UID, "event.message", eventInfo.Message, "error", err)
	}
	return err
}

func parseMCDMinMaxFromShootWorker(worker *unstructured.Unstructured) (mcdMinMaxMap map[string]sizeLimits, err error) {
	mcdMinMaxMap = make(map[string]sizeLimits)
	statusMap, err := GetInnerMap(worker.UnstructuredContent(), "status")
	if err != nil {
		err = fmt.Errorf("cannot get worker.status for %q: %w", worker.GetName(), err)
		return
	}
	mcdListObj := statusMap["machineDeployments"]
	if mcdListObj == nil {
		err = fmt.Errorf("cannot find machineDeployments key in status map: %w", ErrKeyNotFound)
		return
	}
	mcdList := mcdListObj.([]any)
	for _, mcd := range mcdList {
		mcdMap := mcd.(map[string]any)
		mcdName := mcdMap["name"].(string)
		mms := sizeLimits{
			Name: mcdName,
			Min:  int(mcdMap["minimum"].(int64)),
			Max:  int(mcdMap["maximum"].(int64)),
		}
		mcdMinMaxMap[mcdName] = mms
	}
	return
}

func (r *defaultRecorder) processWorker(worker *unstructured.Unstructured) error {
	workerPoolsMap, err := parseWorkerPools(worker)
	if err != nil {
		return fmt.Errorf("cannot parse worker pools for worker %q of generation %d: %w", worker.GetName(), worker.GetGeneration(), err)
	}
	for k, v := range workerPoolsMap {
		r.workerPoolsMap.Store(k, v)
	}
	mcdMinMaxMap, err := parseMCDMinMaxFromShootWorker(worker)
	if err != nil {
		return err
	}
	slog.Debug("processWorker: finished parseMCDMinMaxFromShootWorker", "mcdMinMaxMap", mcdMinMaxMap)

	for name, minMax := range mcdMinMaxMap {
		currMinMax, ok := r.mcdMinMaxMap.Load(name)
		if ok && currMinMax == minMax {
			continue
		}
		slog.Debug("processWorker: storing MinMax for MCD.", "mcd.Name", name, "mcd.Min", minMax.Min, "mcd.Max", minMax.Max)
		r.mcdMinMaxMap.Store(name, minMax)
	}
	return nil
}

func (r *defaultRecorder) onAddWorker(obj interface{}) {
	worker := obj.(*unstructured.Unstructured)
	slog.Info("Worker obj added.", "worker", worker.GetName(), "worker.Generation", worker.GetGeneration())
	err := r.processWorker(worker)
	if err != nil {
		slog.Error("onAddWorker failed", "error", err)
	}
}

func (r *defaultRecorder) onUpdateWorker(_, new any) {
	worker := new.(*unstructured.Unstructured)
	slog.Info("Worker obj changed.", "worker", worker.GetName(), "worker.Generation", worker.GetGeneration())
	if new == nil {
		slog.Error("onUpdateWorker: new worker is nil")
		return
	}
	err := r.processWorker(worker)
	if err != nil {
		slog.Error("onUpdateWorker failed", "error", err)
	}
}

//func getMachineDeploymentUpdateTime(worker *unstructured.Unstructured) (*time.Time, error) {
//	statusMap, err := GetInnerMap(worker.UnstructuredContent(), "status")
//	if err != nil {
//		return nil, fmt.Errorf("cannot get worker.status for %q: %w", worker.GetName(), err)
//	}
//	timestampStr, ok := statusMap["machineDeploymentsLastUpdateTime"].(string)
//	if !ok {
//		return nil, fmt.Errorf("cannot get worker.status.machineDeploymentsLastUpdateTime for %q", worker.GetName())
//	}
//	timestamp, err := time.Parse(time.RFC3339, timestampStr)
//	if err != nil {
//		return nil, fmt.Errorf("cannot parse worker.status.machineDeploymentsLastUpdateTime %q for worker %q", timestampStr, worker.GetName())
//	}
//	return &timestamp, err
//}

//func getGardenerTimestamp(worker *unstructured.Unstructured) (*time.Time, error) {
//	annotationsMap, err := GetInnerMap(worker.UnstructuredContent(), "metadata", "annotations")
//	if err != nil {
//		return nil, fmt.Errorf("cannot get metadata.annotations from worker %q: %w", worker.GetName(), err)
//	}
//	timeStampStr := annotationsMap["gardener.cloud/timestamp"].(string)
//	gardenerTimeStamp, err := time.Parse(time.RFC3339Nano, timeStampStr)
//	if err != nil {
//		return nil, fmt.Errorf("cannot parse timeStamp %q: %w", timeStampStr, err)
//	}
//	return &gardenerTimeStamp, nil
//}

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

func (r *defaultRecorder) Start(ctx context.Context) error {
	err := r.connChecker.TestConnection(ctx)
	if err != nil {
		slog.Error("connection check failed", "error", err)
		return err
	}
	err = r.dataAccess.Init()
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

	stopCh := ctx.Done()
	r.stopCh = stopCh
	r.runInformers(stopCh)

	slog.Info("Waiting for caches to be synced...")
	if !cache.WaitForCacheSync(ctx.Done(),
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
	slog.Info("Informer caches are synced")
	context.AfterFunc(ctx, func() {
		_ = r.Close()
	})
	return nil
}

func (r *defaultRecorder) runInformers(stopCh <-chan struct{}) {
	slog.Info("Calling informerFactory.Start()")
	slog.Info("Calling controllerInformerFactory.Start()")
	r.informerFactory.Start(stopCh)
	r.controlInformerFactory.Start(stopCh)
}

func (r *defaultRecorder) getWorkerPool(machineDeploymentName string) (wp scalehist.WorkerPool, err error) {
	found := false
	r.workerPoolsMap.Range(func(_, p any) bool {
		pool := p.(scalehist.WorkerPool)
		nameWithoutZone := r.params.ShootNameSpace + "-" + pool.Name
		if strings.HasPrefix(machineDeploymentName, nameWithoutZone) {
			wp = pool
			found = true
			return false
		}
		return true
	})
	if !found {
		err = fmt.Errorf("could not find worker pool for MachineDeployment %q", machineDeploymentName)
	}
	return
}

func (r *defaultRecorder) processMCD(mcdOld, mcdNew *unstructured.Unstructured) error {
	now := time.Now()

	mcdName := mcdNew.GetName()
	mcdGeneration := mcdNew.GetGeneration()

	// if MCD generation is same between old and new, then skip
	//if mcdOld != nil && mcdNew != nil && mcdOld.GetGeneration() == mcdNew.GetGeneration() {
	//	slog.Info("processMCD: Skipping update since old & new MCD have same generation.", "mcd.Name", mcdName, "mcd.Generation", mcdNew.GetGeneration())
	//	return nil
	//}

	// check whether min max size for MCDs (got from worker) has been populated.
	val, ok := r.mcdMinMaxMap.Load(mcdName)
	if !ok {
		return fmt.Errorf("min/max size not yet available for mcd %q: %w", mcdName, ErrKeyNotFound)
	}
	mcdSizeLimits := val.(sizeLimits)

	var targetSize int
	replicas, err := GetInnerMapValue(mcdNew.UnstructuredContent(), "spec", "replicas")
	if err != nil {
		replicas = 0
		targetSize = 0
		//return fmt.Errorf("cannot get the .Spec.replicas from mcdNew %q: %w", mcdName, err)
	} else {
		targetSize = int(replicas.(int64))
	}

	var currentSize int
	if mcdOld != nil {
		replicas, err := GetInnerMapValue(mcdOld.UnstructuredContent(), "spec", "replicas")
		if err != nil {
			replicas = 0
			currentSize = 0
		} else {
			currentSize = int(replicas.(int64))
		}
	} else {
		statusMap, err := GetInnerMap(mcdNew.UnstructuredContent(), "status")
		if err != nil {
			return fmt.Errorf("cannot get the status map from the mcdNew %q: %w", mcdName, err)
		}
		availableReplicas, ok := statusMap["availableReplicas"]
		if ok {
			slog.Info("setting node group CurrentSize to mcd.availableReplicas for nodegroup", "ng.Name", mcdName, "mcd.availableReplicas", availableReplicas)
			currentSize = int(availableReplicas.(int64))
		}
	}

	var lastUpdateTime time.Time
	if mcdOld == nil { // fresh guy
		lastUpdateTime = mcdNew.GetCreationTimestamp().UTC()
	} else {
		lastUpdateTime = now.UTC()
	}
	//conditions := getNodeConditionsFromUnstructuredMCD(mcdNew)
	//if conditions == nil {
	//	lastUpdateTime = time.Now().UTC()
	//} else {
	//	lastUpdateTime = getLastUpdateTime(conditions)
	//}
	creationTimestamp := lastUpdateTime

	pool, err := r.getWorkerPool(mcdName)
	if err != nil {
		return err
	}
	zoneIndexStr := strings.TrimPrefix(mcdName, r.params.ShootNameSpace+"-"+pool.Name+"-z")
	zoneIndex, err := strconv.Atoi(zoneIndexStr) // TODO: rename to zoneNum
	if err != nil {
		return fmt.Errorf("cannot parse %q as zone index from mcd Name %q", zoneIndexStr, mcdName)
	}
	zone := pool.Zones[zoneIndex-1]
	//			MaxSize: int(mcdMap["maximum"].(int64)),
	ng := scalehist.NodeGroupInfo{
		Name:              mcdName,
		CreationTimestamp: creationTimestamp,
		CurrentSize:       currentSize,
		TargetSize:        targetSize,
		MinSize:           mcdSizeLimits.Min,
		MaxSize:           mcdSizeLimits.Max,
		Zone:              zone,
		MachineType:       pool.MachineType,
		Architecture:      pool.Architecture,
		ShootGeneration:   pool.ShootGeneration,
		MCDGeneration:     mcdGeneration,
		PoolName:          pool.Name,
		PoolMin:           pool.Minimum,
		PoolMax:           pool.Maximum,
	}
	ng.Hash = ng.GetHash()

	oldNgHash, err := r.dataAccess.GetNodeGroupHash(ng.Name)
	if err != nil {
		return fmt.Errorf("cannot get the hash value for nodegroup %q from db: %w", mcdName, err)
	}
	if oldNgHash != "" && oldNgHash == ng.Hash {
		slog.Info("no change in nodegroup hash. skipping insert", "ng.Name", ng.Name)
		return nil
	}
	_, err = r.dataAccess.StoreNodeGroup(ng)
	r.mcdHashesMap.Store(ng.Name, ng.Hash)
	return err

}

func (r *defaultRecorder) onAddMCD(obj interface{}) {
	mcd := obj.(*unstructured.Unstructured)
	InvokeOrScheduleFunc("onAddMCD", 5*time.Second, mcd, func(mcd *unstructured.Unstructured) error {
		err := r.processMCD(nil, mcd)
		if err != nil {
			slog.Error("onAddMCD failed.", "error", err)
		}
		return err
	})
}

func (r *defaultRecorder) onUpdateMCD(old interface{}, new interface{}) {
	oldObj := old.(*unstructured.Unstructured)
	newObj := new.(*unstructured.Unstructured)
	InvokeOrScheduleFunc("onUpdateMCD", 5*time.Second, newObj, func(mcd *unstructured.Unstructured) error {
		err := r.processMCD(oldObj, newObj)
		if err != nil {
			slog.Error("onUpdateMCD Failed.", "error", err)
		}
		return err
	})
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

func (r *defaultRecorder) onDeleteMCD(obj interface{}) {
	mcdObj := obj.(*unstructured.Unstructured)
	name := mcdObj.GetName()
	hashVal, ok := r.mcdHashesMap.Load(name)
	if !ok {
		slog.Error("onDeleteMCD: cannot get the in-mem cached hash for MCD name.", "ng.Name", name)
		return
	}
	ngHash := hashVal.(string)
	slog.Info("onDeleteMCD: Updating the DeletionTimestamp for NodeGroup associated with MCD.", "ng.Name", name, "ng.Hash", ngHash)
	_, err := r.dataAccess.UpdateNodeGroupDeletionTimestamp(ngHash, mcdObj.GetDeletionTimestamp().UTC())
	if err != nil {
		slog.Error("cannot update the deletion timestamp for the nodegroup", "nodegroup.name", mcdObj.GetName())
	}

	r.mcdMinMaxMap.Delete(name)
	r.mcdHashesMap.Delete(name)
}

func getCACommand(deployment *unstructured.Unstructured) ([]string, error) {
	parentMap := deployment.UnstructuredContent()
	specMap, err := GetInnerMap(parentMap, "spec", "template", "spec")
	if err != nil {
		return []string{}, err
	}
	caContainer := (specMap["containers"].([]interface{})[0]).(map[string]interface{})
	return lo.Map(caContainer["command"].([]interface{}), func(item interface{}, _ int) string {
		return item.(string)
	}), nil
}

func processCACommand(caCommand []string) (result map[string]string) {
	result = make(map[string]string)
	for _, command := range caCommand {
		command = strings.TrimPrefix(command, "--")
		commandParts := strings.Split(command, "=")
		if len(commandParts) < 2 {
			continue
		}
		key := commandParts[0]
		value := commandParts[1]
		if key == "expander" || key == "max-nodes-total" {
			result[key] = value
		}
	}
	return
}

func (r *defaultRecorder) onAddDeployment(obj interface{}) {
	deployment := obj.(*unstructured.Unstructured)
	if deployment.GetName() != "cluster-autoscaler" {
		return
	}
	caCommands, err := getCACommand(deployment)
	if err != nil {
		slog.Error("cannot get the CA command from deployment", "error", err)
		return
	}
	processedCACommand := processCACommand(caCommands)
	maxNodesTotal, err := strconv.Atoi(processedCACommand["max-nodes-total"])
	if err != nil {
		slog.Error("cannot convert maxNodesTotal string to int", "error", err)
	}

	caSettings := scalehist.CASettingsInfo{
		Expander:      processedCACommand["expander"],
		MaxNodesTotal: maxNodesTotal,
	}
	caSettings.Hash = caSettings.GetHash()
	latestCaDeployment, err := r.dataAccess.GetLatestCADeployment()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("cannot get the latest ca deployment stored in db", "error", err)
			return
		}
	}
	if latestCaDeployment != nil {
		caSettings.Priorities = latestCaDeployment.Priorities
	}

	if latestCaDeployment == nil || latestCaDeployment.Hash != caSettings.Hash {
		_, err := r.dataAccess.StoreCADeployment(caSettings)
		if err != nil {
			slog.Error("cannot store ca settings in ca_settings_info", "error", err)
			return
		}
	}
	return

}

func (r *defaultRecorder) onUpdateDeployment(_, newObj interface{}) {
	r.onAddDeployment(newObj)
}

func getPrirotiesFromCAConfig(obj *unstructured.Unstructured) (string, error) {
	caConfig := obj.UnstructuredContent()
	dataMap, err := GetInnerMap(caConfig, "data")
	if err != nil {
		return "", err
	}
	return dataMap["priorities"].(string), nil
}

func (r *defaultRecorder) onAddConfigMap(obj interface{}) {
	configMap := obj.(*unstructured.Unstructured)
	if configMap.GetName() != "cluster-autoscaler-priority-expander" {
		return
	}
	priorities, err := getPrirotiesFromCAConfig(configMap)
	if err != nil {
		slog.Error("cannot get the priorities from priority-expander config map", "error", err)
	}
	fmt.Printf("ConfigMap Priorities : %s\n", priorities)
	caSettings := scalehist.CASettingsInfo{
		Priorities: priorities,
	}
	caSettings.Hash = caSettings.GetHash()
	latestCaDeployment, err := r.dataAccess.GetLatestCADeployment()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("cannot get the latest ca deployment stored in db", "error", err)
			return
		}
	}
	if latestCaDeployment != nil {
		caSettings.MaxNodesTotal = latestCaDeployment.MaxNodesTotal
		caSettings.Expander = latestCaDeployment.Expander
	}
	if latestCaDeployment == nil || latestCaDeployment.Hash != caSettings.Hash {
		_, err = r.dataAccess.StoreCADeployment(caSettings)
		if err != nil {
			slog.Error("cannot store ca settings in ca_settings_info", "error", err)
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

func (r *defaultRecorder) StoreLatestNodeGroupCurrentSizeAndTargetSize(ngName string, timestamp time.Time, currentSize, targetSize int) (err error) {
	currentNg, err := r.dataAccess.LoadLatestNodeGroup(ngName)
	if err != nil {
		slog.Error("cannot load  the nodegroup", "nodegroup", ngName, "error", err)
		return
	}
	currentNg.TargetSize = targetSize
	currentNg.CurrentSize = currentSize
	currentNg.CreationTimestamp = timestamp

	if currentNg.Hash == currentNg.GetHash() {
		return
	}
	currentNg.Hash = currentNg.GetHash()
	//TODO update from currentMCD map
	_, err = r.dataAccess.StoreNodeGroup(currentNg)
	if err != nil {
		slog.Error("cannot insert the nodegroup", "nodegroup", ngName, "error", err)
		return
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
	event := scalehist.EventInfo{
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
		allocatableCount = int(*(csiNode.Spec.Drivers[0].Allocatable.Count))

	}
	r.nodeAllocatableVolumes.Store(csiNode.Name, allocatableCount)
}

func (r *defaultRecorder) onUpdateCSINode(_ interface{}, newObj interface{}) {
	r.onAddCSINode(newObj)
}

func (r *defaultRecorder) onDeleteCSINode(obj interface{}) {
	csiNode := obj.(*storagev1.CSINode)
	r.nodeAllocatableVolumes.Delete(csiNode.Name)
}

func parseIntOrStr(val any) (intstr.IntOrString, error) {
	if reflect.TypeOf(val).Kind() == reflect.String {
		return intstr.Parse(val.(string)), nil
	}
	if reflect.TypeOf(val).Kind() == reflect.Int64 {
		return intstr.FromInt32(int32(val.(int64))), nil
	}
	if reflect.TypeOf(val).Kind() == reflect.Int32 {
		return intstr.FromInt32(val.(int32)), nil
	}
	return intstr.IntOrString{}, fmt.Errorf("cannot parse %v as int or string", val)
}

func getLastUpdateTimeForNode(n *corev1.Node) (lastUpdateTime time.Time) {
	lastUpdateTime = n.ObjectMeta.CreationTimestamp.UTC()
	for _, condition := range n.Status.Conditions {
		if condition.LastTransitionTime.After(lastUpdateTime) {
			lastUpdateTime = condition.LastTransitionTime.Time
		}
	}
	//if p.Status.StartTime.After(lastUpdateTime) {
	//	lastUpdateTime = p.Status.StartTime.Time.UTC()
	//}
	return
}
func (r *defaultRecorder) nodeInfoFromNode(n *corev1.Node) scalehist.NodeInfo {
	var ni scalehist.NodeInfo
	ni.Name = n.Name
	ni.Namespace = n.Namespace
	ni.CreationTimestamp = getLastUpdateTimeForNode(n)
	ni.ProviderID = n.Spec.ProviderID
	ni.Labels = n.Labels
	// Removing this label as it just takes useless space: "node.machine.sapcloud.io/last-applied-anno-labels-taints"
	delete(ni.Labels, "node.machine.sapcloud.io/last-applied-anno-labels-taints")
	ni.Taints = n.Spec.Taints
	ni.Allocatable = n.Status.Allocatable
	ni.Capacity = n.Status.Capacity
	allocVolumes, _ := r.nodeAllocatableVolumes.Load(ni.Name)
	if allocVolumes != nil {
		ni.AllocatableVolumes = allocVolumes.(int)
	}
	ni.Hash = ni.GetHash()
	return ni
}

func getLastUpdateTimeForPod(p *corev1.Pod) (lastUpdateTime time.Time) {
	lastUpdateTime = p.ObjectMeta.CreationTimestamp.UTC()
	for _, condition := range p.Status.Conditions {
		if condition.LastTransitionTime.After(lastUpdateTime) {
			lastUpdateTime = condition.LastTransitionTime.Time
		}
	}
	//if p.Status.StartTime.After(lastUpdateTime) {
	//	lastUpdateTime = p.Status.StartTime.Time.UTC()
	//}
	return
}

func podInfoFromPod(p *corev1.Pod) scalehist.PodInfo {
	var pi scalehist.PodInfo
	pi.UID = string(p.UID)
	pi.Name = p.Name
	pi.Namespace = p.Namespace
	pi.CreationTimestamp = getLastUpdateTimeForPod(p)
	pi.NodeName = p.Spec.NodeName
	pi.Labels = p.Labels
	pi.Requests = scalehist.CumulatePodRequests(p)
	pi.Spec = p.Spec
	pi.PodScheduleStatus = ComputePodScheduleStatus(p)
	pi.Hash = pi.GetHash()
	return pi
}
