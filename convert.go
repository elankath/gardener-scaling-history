package gsh

import (
	"fmt"
	"github.com/elankath/gardener-scaling-common"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"reflect"
	"time"
)

const PoolLabel = "worker.gardener.cloud/pool"
const PoolLabelAlt = "worker_gardener_cloud_pool"

func MachineDeploymentInfoFromUnstructured(mcd *unstructured.Unstructured, snapshotTime time.Time) (mcdInfo gsc.MachineDeploymentInfo, err error) {
	mcdName := mcd.GetName()
	replicasPath := []string{"spec", "replicas"}
	replicasVal, found, err := unstructured.NestedInt64(mcd.UnstructuredContent(), replicasPath...)
	if err != nil {
		err = fmt.Errorf("error looking up replicasPath %q from unstructured MachineDeployment %q: %w", replicasPath, mcdName, err)
		return
	}
	var replicas int
	if found {
		replicas = int(replicasVal)
	}

	templateSpecPath := []string{"spec", "template", "spec"}
	templateSpecMap, found, err := unstructured.NestedMap(mcd.UnstructuredContent(), templateSpecPath...)
	if err != nil {
		err = fmt.Errorf("error looking up templateSpecPath %q from unstructured MachineDeployment %q: %w", templateSpecPath, mcdName, err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find  templateSpecPath %q in unstructured MachineDeployment %q", templateSpecPath, mcdName)
		return
	}
	labelsLookupPath := []string{"nodeTemplate", "metadata", "labels"}
	labelsMap, found, err := unstructured.NestedStringMap(templateSpecMap, labelsLookupPath...)
	if err != nil {
		err = fmt.Errorf("error looking up labels map via path %q in MachineDeployment %q: %w", labelsLookupPath, mcdName, err)
		return
	}
	if !found {
		err = fmt.Errorf("cant find labels map via path %q in MachineDeployment %q", labelsLookupPath, mcdName)
		return
	}
	poolName, ok := labelsMap[PoolLabel]
	if !ok {
		err = fmt.Errorf("cant find pool label %q in labels of MachineDeployment %q", PoolLabel, mcdName)
		return
	}
	var zone string
	for _, zoneLabel := range ZoneLabels {
		z, ok := labelsMap[zoneLabel]
		if ok {
			zone = z
			break
		}
	}
	rollingUpdatePath := []string{"spec", "strategy", "rollingUpdate"}
	rollingUpdateMap, found, err := unstructured.NestedMap(mcd.UnstructuredContent(), rollingUpdatePath...)
	if err != nil {
		err = fmt.Errorf("error lookingup rollingUpdate map via path %q in MachineDeployment %q: %w", rollingUpdatePath, mcdName, err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find rollingUpdate map via path %q in MachineDeployment %q", rollingUpdatePath, mcdName)
		return
	}
	maxSurgeVal, ok := rollingUpdateMap["maxSurge"]
	if !ok {
		err = fmt.Errorf("cant find spec.strategy.rollingUpdat.maxSurge in MachineDeployment %q", mcdName)
	}
	maxSurge, err := asIntOrString(maxSurgeVal)
	if err != nil {
		return
	}
	maxUnavailableVal, ok := rollingUpdateMap["maxUnavailable"]
	if !ok {
		err = fmt.Errorf("cant find spec.strategy.rollingUpdat.maxUnavailable in MachineDeployment %q", mcdName)
	}
	maxUnavailable, err := asIntOrString(maxUnavailableVal)
	if err != nil {
		return
	}
	machineClassLookupPath := []string{"class", "name"}
	machineClassName, found, err := unstructured.NestedString(templateSpecMap, machineClassLookupPath...)
	if err != nil {
		err = fmt.Errorf("error lookingg up machineClassName via path %q in MachineDeployment.Spec.Template.Spec of %q: %w", machineClassLookupPath, mcdName, err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find machineClassName via path %q in MachineDeployment.Spec.Template.Spec of %q", machineClassLookupPath, mcdName)
		return
	}
	taintsLookupPath := []string{"nodeTemplate", "spec", "taints"}
	taintsVal, found, err := unstructured.NestedSlice(templateSpecMap, taintsLookupPath...)
	if err != nil {
		err = fmt.Errorf("error loading nested taints inside path %q belonging to machine deployment %q: %w", taintsLookupPath, mcdName, err)
		return
	}
	var taints []corev1.Taint
	if found {
		for _, tv := range taintsVal {
			tvMap := tv.(map[string]any)
			taints = append(taints, corev1.Taint{
				Key:       tvMap["key"].(string),
				Value:     tvMap["value"].(string),
				Effect:    corev1.TaintEffect(tvMap["effect"].(string)),
				TimeAdded: nil,
			})
		}
	}

	mcdInfo = gsc.MachineDeploymentInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: mcd.GetCreationTimestamp().UTC(),
			SnapshotTimestamp: snapshotTime,
			Name:              mcdName,
			Namespace:         mcd.GetNamespace(),
		},
		Replicas:         replicas,
		PoolName:         poolName,
		Zone:             zone,
		MaxSurge:         maxSurge,
		MaxUnavailable:   maxUnavailable,
		MachineClassName: machineClassName,
		Labels:           labelsMap,
		Taints:           taints,
	}
	mcdInfo.Hash = mcdInfo.GetHash()
	return
}

func MachineClassInfoFromUnstructured(mcc *unstructured.Unstructured, snapshotTime time.Time) (mccInfo MachineClassInfo, err error) {
	mccName := mcc.GetName()

	instanceType, err := findStringVal("MachineClass "+mccName, mcc.UnstructuredContent(), []string{"nodeTemplate", "instanceType"})
	if err != nil {
		return
	}
	region, err := findStringVal("MachineClass "+mccName, mcc.UnstructuredContent(), []string{"nodeTemplate", "region"})
	if err != nil {
		return
	}

	zone, err := findStringVal("MachineClass "+mccName, mcc.UnstructuredContent(), []string{"nodeTemplate", "zone"})
	if err != nil {
		return
	}

	capacityPath := []string{"nodeTemplate", "capacity"}
	capacityMap, found, err := unstructured.NestedMap(mcc.UnstructuredContent(), capacityPath...)
	if err != nil {
		err = fmt.Errorf("error looking up capacity path %q in %q: %w", capacityPath, mccName, err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find capacity map in path %q in %q", capacityMap, mccName)
		return
	}
	resources, err := ResourceListFromMap(capacityMap)
	if err != nil {
		return
	}

	// worker.gardener.cloud/pool
	providerSpecLabelsPath := []string{"providerSpec", "labels"}
	labelsMap, found, err := unstructured.NestedStringMap(mcc.UnstructuredContent(), providerSpecLabelsPath...)
	if err != nil {
		err = fmt.Errorf("error looking up path %q in %q: %w", providerSpecLabelsPath, mccName, err)
		return
	}
	if !found {
		providerSpecLabelsPath = []string{"providerSpec", "tags"} //fallback
		labelsMap, found, err = unstructured.NestedStringMap(mcc.UnstructuredContent(), providerSpecLabelsPath...)
		if !found {
			err = fmt.Errorf("cannot find value in path %q in %q", providerSpecLabelsPath, mccName)
			return
		}
	}
	maps.Copy(labelsMap, mcc.GetLabels())

	poolName, ok := labelsMap[PoolLabelAlt]
	if !ok {
		poolName, ok = labelsMap[PoolLabel]
		if !ok {
			err = fmt.Errorf("cant find pool label %q or %q in labels of MachineClass %q", PoolLabel, PoolLabelAlt, mccName)
			return
		}
	}

	mccInfo = MachineClassInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: mcc.GetCreationTimestamp().UTC(),
			SnapshotTimestamp: snapshotTime,
			Name:              mccName,
			Namespace:         mcc.GetNamespace(),
		},
		InstanceType: instanceType,
		PoolName:     poolName,
		Region:       region,
		Zone:         zone,
		Labels:       labelsMap,
		Capacity:     resources,
	}
	mccInfo.Hash = mccInfo.GetHash()
	return
}

func asIntOrString(val any) (target intstr.IntOrString, err error) {
	switch v := val.(type) {
	case int32:
		target = intstr.FromInt32(v)
	case int64:
		target = intstr.FromInt32(int32(v))
	case string:
		return intstr.FromString(v), nil
	default:
		err = fmt.Errorf("cannot parse value %q as intstr.IntOrString", val)
	}
	return
}

func NodeInfoFromNode(n *corev1.Node, allocatableVolumes int) gsc.NodeInfo {
	var ni gsc.NodeInfo
	ni.Name = n.Name
	ni.Namespace = n.Namespace
	ni.CreationTimestamp = n.CreationTimestamp.UTC()
	ni.SnapshotTimestamp = getLastUpdateTimeForNode(n)
	ni.ProviderID = n.Spec.ProviderID
	ni.Labels = n.Labels
	// Removing this label as it just takes useless space: "node.machine.sapcloud.io/last-applied-anno-labels-taints"
	delete(ni.Labels, "node.machine.sapcloud.io/last-applied-anno-labels-taints")
	ni.Taints = n.Spec.Taints
	ni.Allocatable = n.Status.Allocatable
	ni.Capacity = n.Status.Capacity
	ni.AllocatableVolumes = allocatableVolumes
	ni.Hash = ni.GetHash()
	return ni
}

func getLastUpdateTimeForNode(n *corev1.Node) (lastUpdateTime time.Time) {
	lastUpdateTime = n.ObjectMeta.CreationTimestamp.UTC()
	for _, condition := range n.Status.Conditions {
		lastTransitionTime := condition.LastTransitionTime.UTC()
		if lastTransitionTime.After(lastUpdateTime) {
			lastUpdateTime = lastTransitionTime
		}
	}
	return
}

func findStringVal(label string, parentMap map[string]any, lookupPath []string) (val string, err error) {
	val, found, err := unstructured.NestedString(parentMap, lookupPath...)
	if err != nil {
		err = fmt.Errorf("error looking up path %q in %q: %w", lookupPath, label, err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find value in path %q in %q", lookupPath, label)
		return
	}
	return
}

func ResourceListFromMap(input map[string]any) (corev1.ResourceList, error) {
	resourceList := corev1.ResourceList{}

	for key, value := range input {
		// Convert the value to a string
		strValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("value for key %s is not a string", key)
		}

		// Parse the string value into a Quantity
		quantity, err := resource.ParseQuantity(strValue)
		if err != nil {
			return nil, fmt.Errorf("error parsing quantity for key %s: %v", key, err)
		}
		quantity, err = gsc.NormalizeQuantity(quantity)
		if err != nil {
			return nil, fmt.Errorf("cannot normalize quantity %q: %w", quantity, err)
		}
		// Assign the quantity to the ResourceList
		resourceList[corev1.ResourceName(key)] = quantity
	}

	return resourceList, nil
}

func WorkerPoolInfosFromUnstructured(worker *unstructured.Unstructured) (map[string]gsc.WorkerPoolInfo, error) {
	specMapObj, found, err := unstructured.NestedMap(worker.UnstructuredContent(), "spec")
	if err != nil {
		return nil, fmt.Errorf("error getting the '.spec' map for worker %q: %w", worker.GetName(), err)
	}
	if !found {
		return nil, fmt.Errorf("cannot find '.spec' map in worker %q: %w", worker.GetName(), gsc.ErrKeyNotFound)
	}
	poolsList := specMapObj["pools"].([]any)

	gardenerTimestamp, err := GardenerTimestampFromUnstructured(worker)
	if err != nil {
		return nil, err
	}
	var poolInfosByName = make(map[string]gsc.WorkerPoolInfo)
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
		wp := gsc.WorkerPoolInfo{
			SnapshotMeta: gsc.SnapshotMeta{
				CreationTimestamp: worker.GetCreationTimestamp().UTC(),
				SnapshotTimestamp: gardenerTimestamp,
				Name:              poolMap["name"].(string),
				Namespace:         worker.GetNamespace(),
			},
			Minimum:        int(poolMap["minimum"].(int64)),
			Maximum:        int(poolMap["maximum"].(int64)),
			MaxSurge:       maxSurge,
			MaxUnavailable: maxUnavailable,
			MachineType:    poolMap["machineType"].(string),
			Architecture:   poolMap["architecture"].(string),
			Zones:          zones,
		}
		wp.Hash = wp.GetHash()
		poolInfosByName[wp.Name] = wp
	}
	return poolInfosByName, nil
}

func GardenerTimestampFromUnstructured(worker *unstructured.Unstructured) (gardenerTimeStamp time.Time, err error) {
	annotationsMap, found, err := unstructured.NestedMap(worker.UnstructuredContent(), "metadata", "annotations")
	if err != nil {
		err = fmt.Errorf("cannot get metadata.annotations from worker %q: %w", worker.GetName(), err)
		return
	}
	if !found {
		err = fmt.Errorf("cannot find metadata.annotations from worker %q: %w", worker.GetName(), gsc.ErrKeyNotFound)
		return
	}
	timeStampVal, ok := annotationsMap["gardener.cloud/timestamp"]
	if !ok {
		err = fmt.Errorf("cannot find 'gardener.cloud/timestamp' in annotations of worker %q: %w", worker.GetName(), gsc.ErrKeyNotFound)
		return
	}
	timeStampStr := timeStampVal.(string)
	gardenerTimeStamp, err = time.Parse(time.RFC3339Nano, timeStampStr)
	if err != nil {
		err = fmt.Errorf("cannot parse timeStamp %q: %w", timeStampStr, err)
		return
	}
	return gardenerTimeStamp.UTC(), nil
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

//func GetMapValue[M map[string]V, V any](m M, keys ...string) (value V, ok bool) {
//	for _, k := range keys {
//		value, ok = m[k]
//		if ok {
//			return
//		}
//	}
//	ok = false
//	return
//}
