package gsh

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/elankath/gardener-scaling-types"
	"golang.org/x/exp/maps"
	"hash"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"slices"
)

func header(prefix string, meta gst.SnapshotMeta) string {
	return fmt.Sprintf("%s(RowID=%d, CreationTimestamp=%s, SnapshotTimestamp=%s, Name=%s, Namespace=%s",
		prefix, meta.RowID, meta.CreationTimestamp, meta.SnapshotTimestamp, meta.Name, meta.Namespace)
}

func (m MachineClassInfo) String() string {
	metaStr := header("MachineClass", m.SnapshotMeta)
	return fmt.Sprintf("%s, InstanceType=%s, PoolName=%s, Region=%s, Zone=%s, Labels=%s, Capacity=%s, Hash=%s)",
		metaStr, m.InstanceType, m.PoolName, m.Region, m.Zone, m.Labels, gst.ResourcesAsString(m.Capacity), m.Hash)
}
func (m MachineClassInfo) GetHash() string {
	int64buf := make([]byte, 8) // 8 bytes for int64

	hasher := md5.New()
	hasher.Write([]byte(m.Name))
	hasher.Write([]byte(m.Namespace))

	binary.BigEndian.PutUint64(int64buf, uint64(m.CreationTimestamp.UnixMilli()))
	hasher.Write(int64buf)

	hasher.Write([]byte(m.InstanceType))
	hasher.Write([]byte(m.PoolName))
	hasher.Write([]byte(m.Region))
	hasher.Write([]byte(m.Zone))

	hashLabels(hasher, m.Labels)
	hashResources(hasher, m.Capacity)

	return hex.EncodeToString(hasher.Sum(nil))
}

//import (
//	"crypto/md5"
//	"encoding/binary"
//	"encoding/hex"
//	"fmt"
//	"golang.org/x/exp/maps"
//	"hash"
//	corev1 "k8s.io/api/core/v1"
//	"k8s.io/apimachinery/pkg/api/resource"
//	"slices"
//	"strconv"
//	"strings"
//)
//
//func (w WorkerPoolInfo) String() string {
//	metaStr := header("WorkerPoolInfo", w.SnapshotMeta)
//	return fmt.Sprintf("%s, MachineType=%d, Architecture=%s, Minimum=%d, Maximum=%d, MaxSurge=%s, MaxUnavailable=%s, Zones=%s, Hash=%s)",
//		metaStr, w.MachineType, w.Architecture, w.Minimum, w.Maximum, w.MaxSurge.String(), w.MaxUnavailable.String(), w.Zones, w.Hash)
//}
//
//func (w WorkerPoolInfo) GetHash() string {
//	hasher := md5.New()
//	hasher.Write([]byte(w.Name))
//	int64buf := make([]byte, 8) // 8 bytes for int64
//
//	binary.BigEndian.PutUint64(int64buf, uint64(w.CreationTimestamp.UnixMilli()))
//	hasher.Write(int64buf)
//
//	hasher.Write([]byte(w.MachineType))
//	hasher.Write([]byte(w.Architecture))
//
//	binary.BigEndian.PutUint64(int64buf, uint64(w.Minimum))
//	hasher.Write(int64buf)
//
//	binary.BigEndian.PutUint64(int64buf, uint64(w.Maximum))
//	hasher.Write(int64buf)
//
//	hasher.Write([]byte(w.MaxSurge.String()))
//	hasher.Write([]byte(w.MaxUnavailable.String()))
//
//	hashSlice(hasher, w.Zones)
//
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//
//func (ng NodeGroupInfo) String() string {
//	return fmt.Sprintf("NodeGroupInfo(Name: %s, PoolName: %s, Zone: %s, TargetSize: %d, MinSize: %d, MaxSize: %d)",
//		ng.Name, ng.PoolName, ng.Zone, ng.TargetSize, ng.MinSize, ng.MaxSize)
//}
//
//func (ng NodeGroupInfo) GetHash() string {
//	hasher := md5.New()
//	hasher.Write([]byte(ng.Name))
//	int64buf := make([]byte, 8) // 8 bytes for int64
//
//	binary.BigEndian.PutUint64(int64buf, uint64(ng.TargetSize))
//	hasher.Write(int64buf)
//
//	binary.BigEndian.PutUint64(int64buf, uint64(ng.MinSize))
//	hasher.Write(int64buf)
//
//	binary.BigEndian.PutUint64(int64buf, uint64(ng.MaxSize))
//	hasher.Write(int64buf)
//
//	hasher.Write([]byte(ng.Zone))
//	hasher.Write([]byte(ng.PoolName))
//
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//
//func header(prefix string, meta SnapshotMeta) string {
//	return fmt.Sprintf("%s(RowID=%d, CreationTimestamp=%s, SnapshotTimestamp=%s, Name=%s, Namespace=%s",
//		prefix, meta.RowID, meta.CreationTimestamp, meta.SnapshotTimestamp, meta.Name, meta.Namespace)
//}
//func (m MachineDeploymentInfo) String() string {
//	metaStr := header("MachineDeployment", m.SnapshotMeta)
//	return fmt.Sprintf("%s, Replicas=%d, PoolName=%s, Zone=%s, MaxSurge=%s, MaxUnavailable=%s, MachineClassName=%s, Hash=%s)",
//		metaStr, m.Replicas, m.PoolName, m.Zone, m.MaxSurge.String(), m.MaxUnavailable.String(), m.MachineClassName, m.Hash)
//}
//
//func (m MachineDeploymentInfo) GetHash() string {
//	int64buf := make([]byte, 8) // 8 bytes for int64
//
//	hasher := md5.New()
//	hasher.Write([]byte(m.Name))
//	hasher.Write([]byte(m.Namespace))
//
//	hasher.Write([]byte(m.PoolName))
//	hasher.Write([]byte(m.Zone))
//
//	binary.BigEndian.PutUint64(int64buf, uint64(m.Replicas))
//	hasher.Write(int64buf)
//
//	hasher.Write([]byte(m.MaxSurge.String()))
//	hasher.Write([]byte(m.MaxUnavailable.String()))
//	hasher.Write([]byte(m.MachineClassName))
//
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//
//func (n NodeInfo) String() string {
//	return fmt.Sprintf(
//		"NodeInfo(Name=%s, Namespace=%s, CreationTimestamp=%s, ProviderID=%s, Labels=%s, Taints=%s, Allocatable=%s, Capacity=%s)",
//		n.Name, n.Namespace, n.CreationTimestamp, n.ProviderID, n.Labels, n.Taints, ResourcesAsString(n.Allocatable), ResourcesAsString(n.Capacity))
//}
//
//func (n NodeInfo) GetHash() string {
//	hasher := md5.New()
//	hasher.Write([]byte(n.Name))
//	hasher.Write([]byte(n.Namespace))
//	hashLabels(hasher, n.Labels)
//	for _, t := range n.Taints {
//		hasher.Write([]byte(t.Key))
//		hasher.Write([]byte(t.Value))
//		hasher.Write([]byte(t.Effect))
//	}
//	hashResources(hasher, n.Allocatable)
//	hashResources(hasher, n.Capacity)
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//
//func CmpNodeInfoDescending(a, b NodeInfo) int {
//	return b.CreationTimestamp.Compare(a.CreationTimestamp)
//}
//
//func IsEqualNodeInfo(a, b NodeInfo) bool {
//	return a.Name == b.Name && a.Namespace == b.Namespace &&
//		a.AllocatableVolumes == b.AllocatableVolumes &&
//		maps.Equal(a.Labels, b.Labels) && slices.EqualFunc(a.Taints, b.Taints, IsEqualTaint) &&
//		maps.Equal(a.Allocatable, b.Allocatable) && maps.Equal(a.Capacity, b.Capacity) &&
//		a.Hash == b.Hash
//}
//

func IsEqualQuantity(a, b resource.Quantity) bool {
	return a.Equal(b)
}

//
//func IsEqualPodInfo(a, b PodInfo) bool {
//	return a.GetHash() == b.GetHash()
//}
//
//func IsEqualTaint(a, b corev1.Taint) bool {
//	return a.Key == b.Key && a.Value == b.Value && a.Effect == b.Effect
//}
//
//func (p PodInfo) String() string {
//	metaStr := header("PodInfo", p.SnapshotMeta)
//	return fmt.Sprintf("%s, UID=%s, NodeName=%s, NominatedNodeName=%s, Labels=%s, Requests=%s, Hash=%s)",
//		metaStr, p.UID, p.NodeName, p.NominatedNodeName, p.Labels, ResourcesAsString(p.Requests), p.Hash)
//}
//
//func (p PodInfo) GetHash() string {
//	hasher := md5.New()
//	hasher.Write([]byte(p.Name))
//	hasher.Write([]byte(p.Namespace))
//	hasher.Write([]byte(p.NodeName))
//	hasher.Write([]byte(p.NominatedNodeName))
//	hashLabels(hasher, p.Labels)
//	hasher.Write([]byte(p.Spec.SchedulerName))
//	int64buf := make([]byte, 8) // 8 bytes for int64
//	binary.BigEndian.PutUint64(int64buf, uint64(p.PodScheduleStatus))
//	hasher.Write(int64buf)
//
//	binary.BigEndian.PutUint64(int64buf, uint64(p.CreationTimestamp.UnixMilli()))
//	hasher.Write(int64buf)
//
//	slices.SortFunc(p.Spec.Containers, func(a, b corev1.Container) int {
//		return strings.Compare(a.Name, b.Name)
//	})
//	for _, c := range p.Spec.Containers {
//		hasher.Write([]byte(c.Name))
//		hashSlice(hasher, c.Args)
//		hashSlice(hasher, c.Command)
//		hasher.Write([]byte(c.Image))
//		for _, e := range c.Env {
//			hasher.Write([]byte(e.Name))
//			hasher.Write([]byte(e.Value))
//		}
//	}
//	hashResources(hasher, p.Requests)
//	slices.SortFunc(p.Spec.Tolerations, func(a, b corev1.Toleration) int {
//		return strings.Compare(a.Key, b.Key)
//	})
//	for _, t := range p.Spec.Tolerations {
//		hasher.Write([]byte(t.Key))
//		hasher.Write([]byte(t.Operator))
//		hasher.Write([]byte(t.Value))
//		hasher.Write([]byte(t.Effect))
//		//TODO: TolerationSeconds to Hash ??
//	}
//	slices.SortFunc(p.Spec.TopologySpreadConstraints, func(a, b corev1.TopologySpreadConstraint) int {
//		return strings.Compare(a.TopologyKey, b.TopologyKey)
//	})
//	for _, tsc := range p.Spec.TopologySpreadConstraints {
//		binary.BigEndian.PutUint64(int64buf, uint64(tsc.MaxSkew))
//		hasher.Write(int64buf)
//
//		hasher.Write([]byte(tsc.TopologyKey))
//		hasher.Write([]byte(tsc.WhenUnsatisfiable))
//		if tsc.LabelSelector != nil {
//			hasher.Write([]byte(tsc.LabelSelector.String()))
//		}
//		if tsc.MinDomains != nil {
//			binary.BigEndian.PutUint64(int64buf, uint64(*tsc.MinDomains))
//			hasher.Write(int64buf)
//		}
//		if tsc.NodeAffinityPolicy != nil {
//			hasher.Write([]byte(*tsc.NodeAffinityPolicy))
//		}
//		if tsc.NodeTaintsPolicy != nil {
//			hasher.Write([]byte(*tsc.NodeTaintsPolicy))
//		}
//		for _, lk := range tsc.MatchLabelKeys {
//			hasher.Write([]byte(lk))
//		}
//	}
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//func ContainsPod(podUID string, podInfos []PodInfo) bool {
//	return slices.ContainsFunc(podInfos, func(info PodInfo) bool {
//		return info.UID == podUID
//	})
//}
//
//func hashResources(hasher hash.Hash, resources corev1.ResourceList) {
//	keys := maps.Keys(resources)
//	slices.Sort(keys)
//	for _, k := range keys {
//		hashResource(hasher, k, resources[k])
//	}
//}
//func hashSlice[T ~string](hasher hash.Hash, strSlice []T) {
//	for _, t := range strSlice {
//		hasher.Write([]byte(t))
//	}
//}
//
//func hashResourcesOld(hash hash.Hash, resources corev1.ResourceList) {
//	var q resource.Quantity
//	q, ok := resources[corev1.ResourceCPU]
//	if ok {
//		hashResource(hash, corev1.ResourceCPU, q)
//	}
//	q, ok = resources[corev1.ResourceMemory]
//	if ok {
//		hashResource(hash, corev1.ResourceMemory, q)
//	}
//	q, ok = resources[corev1.ResourceStorage]
//	if ok {
//		hashResource(hash, corev1.ResourceStorage, q)
//	}
//	q, ok = resources[corev1.ResourceEphemeralStorage]
//	if ok {
//		hashResource(hash, corev1.ResourceEphemeralStorage, q)
//	}
//}
//

func hashLabels(hasher hash.Hash, labels map[string]string) {
	keys := maps.Keys(labels)
	slices.Sort(keys)
	for _, k := range keys {
		hasher.Write([]byte(k))
		hasher.Write([]byte(labels[k]))
	}
}

func hashResources(hasher hash.Hash, resources corev1.ResourceList) {
	keys := maps.Keys(resources)
	slices.Sort(keys)
	for _, k := range keys {
		hashResource(hasher, k, resources[k])
	}
}

func hashResource(hasher hash.Hash, name corev1.ResourceName, quantity resource.Quantity) {
	hasher.Write([]byte(name))
	rvBytes, _ := quantity.AsCanonicalBytes(nil)
	hasher.Write(rvBytes)
}

//}

//
//func ResourcesAsString(resources corev1.ResourceList) string {
//	if len(resources) == 0 {
//		return ""
//	}
//	var sb strings.Builder
//	sb.WriteString("(")
//	var j int
//	for k, v := range resources {
//		sb.WriteString(string(k))
//		sb.WriteString(":")
//		sb.WriteString(v.String())
//		j++
//		if j != len(resources) {
//			sb.WriteString(",")
//		}
//	}
//	sb.WriteString(")")
//	return sb.String()
//}
//
//func (eI EventInfo) String() string {
//	return fmt.Sprintf("EventInfo : (UID = %s,EventTime = %s, ReportingController = %s, Reason = %s, Message = %s, InvolvedObjectName = %s,InvolvedObjectNamespace = %s, InvolvedObjectUID = %s)",
//		eI.UID, eI.EventTime, eI.ReportingController, eI.Reason, eI.Message, eI.InvolvedObjectName, eI.InvolvedObjectNamespace, eI.InvolvedObjectUID)
//}
//
//func IsResourceListEqual(r1 corev1.ResourceList, r2 corev1.ResourceList) bool {
//	return maps.EqualFunc(r1, r2, func(q1 resource.Quantity, q2 resource.Quantity) bool {
//		return q1.Equal(q2)
//	})
//}
//
//func (rp RecorderParams) String() string {
//	return fmt.Sprintf("RecorderParams(ShootKubeConfigPath:%s,ShootNamespace:%s, SeedKubeConfigPath:%s, DataDBPath:%s, ScheduelerName:%s",
//		rp.ShootKubeConfigPath, rp.ShootNameSpace, rp.SeedKubeConfigPath, rp.DBPath, rp.SchedulerName)
//}
//
//func (ca CASettingsInfo) GetHash() string {
//	hasher := md5.New()
//	hasher.Write([]byte(ca.Expander))
//	maxNodesTotal := strconv.Itoa(ca.MaxNodesTotal)
//	hasher.Write([]byte(maxNodesTotal))
//	hasher.Write([]byte(ca.Priorities))
//	return hex.EncodeToString(hasher.Sum(nil))
//}
//
//func SumResources(resources []corev1.ResourceList) corev1.ResourceList {
//	sumResources := make(corev1.ResourceList)
//	for _, r := range resources {
//		for name, quantity := range r {
//			sumQuantity, ok := sumResources[name]
//			if ok {
//				sumQuantity.Add(quantity)
//				sumResources[name] = sumQuantity
//			} else {
//				sumResources[name] = quantity
//			}
//		}
//	}
//	//memory, ok := sumResources[corev1.ResourceMemory]
//	//if ok && memory.Format == resource.DecimalSI {
//	//	absVal, ok := memory.AsInt64()
//	//	if !ok {
//	//		return nil, fmt.Errorf("cannot get  the absolute value for memory quantity")
//	//	}
//	//	binaryMem, err := resource.ParseQuantity(fmt.Sprintf("%dKi", absVal/1024))
//	//	if err != nil {
//	//		return nil, fmt.Errorf("cannot parse the memory quantity: %w", err)
//	//	}
//	//	sumResources[corev1.ResourceMemory] = binaryMem
//	//}
//	return sumResources
//}
//
//func CumulatePodRequests(pod *corev1.Pod) corev1.ResourceList {
//	sumRequests := make(corev1.ResourceList)
//	for _, container := range pod.Spec.Containers {
//		for name, quantity := range container.Resources.Requests {
//			sumQuantity, ok := sumRequests[name]
//			if ok {
//				sumQuantity.Add(quantity)
//				sumRequests[name] = sumQuantity
//			} else {
//				sumRequests[name] = quantity
//			}
//		}
//	}
//	return sumRequests
//}
