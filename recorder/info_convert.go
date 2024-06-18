package recorder

import (
	"fmt"
	gcr "github.com/elankath/gardener-cluster-recorder"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"log/slog"
	"time"
)

func asMachineDeploymentInfo(mcd *unstructured.Unstructured, snapshotTime time.Time) (mcdInfo gcr.MachineDeploymentInfo, err error) {
	mcdName := mcd.GetName()
	replicasVal, err := GetInnerMapValue(mcd.UnstructuredContent(), "spec", "replicas")
	var replicas int
	if err != nil {
		replicas = 0
		//return fmt.Errorf("cannot get the .Spec.replicas from mcdNew %q: %w", mcdName, err)
	} else {
		replicas = replicasVal.(int)
	}
	specMap, err := GetInnerMap(mcd.UnstructuredContent(), "spec")
	if err != nil {
		return
	}
	templateSpecMap, err := GetInnerMap(specMap, "template", "spec")
	if err != nil {
		return
	}
	labelsMap, err := GetInnerMap(templateSpecMap, "nodeTemplate", "metadata", "labels")
	poolName, ok := labelsMap[PoolLabel]
	if !ok {
		err = fmt.Errorf("cant find pool label %q in labels of MachineDeployment %q", PoolLabel, mcdName)
		return
	}
	var zone string
	for _, zoneLabel := range ZoneLabels {
		z, ok := labelsMap[zoneLabel]
		if ok {
			zone = z.(string)
			break
		}
	}
	rollingUpdateMap, err := GetInnerMap(specMap, "strategy", "rollingUpdate")
	if err != nil {
		return
	}
	maxSurgeVal, ok := rollingUpdateMap["maxSurge"]
	if !ok {
		err = fmt.Errorf("cant find spec.strategy.rollingUpdat.maxSurge in MachineDeployment %q", mcdName)
	}
	slog.Info("obtained maxSurge from MachineDeployment", "MaxSurge", maxSurgeVal, "Name", mcdName)
	maxSurge, err := asIntOrString(maxSurgeVal)
	if err != nil {
		return
	}
	maxUnavailableVal, ok := rollingUpdateMap["maxUnavailable"]
	if !ok {
		err = fmt.Errorf("cant find spec.strategy.rollingUpdat.maxUnavailable in MachineDeployment %q", mcdName)
	}
	slog.Info("obtained maxUnavailable from MachineDeployment", "MaxUnavailable", maxUnavailableVal, "Name", mcdName)
	maxUnavailable, err := asIntOrString(maxUnavailableVal)
	if err != nil {
		return
	}
	machineClassName, err := GetInnerMapValue(templateSpecMap, "class", "name")
	if err != nil {
		err = fmt.Errorf("cant find MachineClassName inside MachineDeployment %q: %w", err)
		return
	}
	//if maxSurgeVal.()
	mcdInfo = gcr.MachineDeploymentInfo{
		SnapshotMeta: gcr.SnapshotMeta{
			CreationTimestamp: mcd.GetCreationTimestamp().UTC(),
			SnapshotTimestamp: snapshotTime,
			Name:              mcdName,
			Namespace:         mcd.GetNamespace(),
		},
		Replicas:         replicas,
		PoolName:         poolName.(string),
		Zone:             zone,
		MaxSurge:         maxSurge,
		MaxUnavailable:   maxUnavailable,
		MachineClassName: machineClassName.(string),
	}
	mcdInfo.Hash = mcdInfo.GetHash()
	return
}

func asIntOrString(val any) (target intstr.IntOrString, err error) {
	switch v := val.(type) {
	case int32:
		target = intstr.FromInt32(v)
	case string:
		return intstr.FromString(v), nil
	default:
		err = fmt.Errorf("cannot parse value %q as intstr.IntOrString", val)
	}
	return
}

func nodeInfoFromNode(n *corev1.Node, allocatableVolumes int) gcr.NodeInfo {
	var ni gcr.NodeInfo
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
	ni.AllocatableVolumes = allocatableVolumes
	ni.Hash = ni.GetHash()
	return ni
}
