package db

import (
	"database/sql"
	"github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-history"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strings"
	"time"
)

type row[I any] interface {
	AsInfo() (I, error)
}

type hashRow struct {
	RowID             int64 `db:"RowID"`
	Name              string
	SnapshotTimestamp int64 `db:"SnapshotTimestamp"`
	Hash              string
}

type workerPoolRow struct {
	RowID             int64 `db:"RowID"`
	CreationTimestamp int64 `db:"CreationTimestamp"`
	SnapshotTimestamp int64 `db:"SnapshotTimestamp"`
	Name              string
	Namespace         string
	MachineType       string `db:"MachineType"`
	Architecture      string
	Minimum           int
	Maximum           int
	MaxSurge          string `db:"MaxSurge"`
	MaxUnavailable    string `db:"MaxUnavailable"`
	Zones             string
	DeletionTimeStamp sql.NullInt64 `db:"DeletionTimeStamp"`
	Hash              string
}

func (r workerPoolRow) AsInfo() (mcdInfo gsc.WorkerPoolInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = timeFromNano(r.DeletionTimeStamp.Int64)
	}
	var zones []string
	if strings.TrimSpace(r.Zones) != "" {
		zones = strings.Split(r.Zones, " ")
	}
	mcdInfo = gsc.WorkerPoolInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: timeFromNano(r.CreationTimestamp),
			SnapshotTimestamp: timeFromNano(r.SnapshotTimestamp),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		MachineType:       r.MachineType,
		Architecture:      r.Architecture,
		Minimum:           r.Minimum,
		Maximum:           r.Maximum,
		MaxSurge:          intstr.Parse(r.MaxSurge),
		MaxUnavailable:    intstr.Parse(r.MaxUnavailable),
		Zones:             zones,
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}

type mcdRow struct {
	RowID             int64 `db:"RowID"`
	CreationTimestamp int64 `db:"CreationTimestamp"`
	SnapshotTimestamp int64 `db:"SnapshotTimestamp"`
	Name              string
	Namespace         string
	Replicas          int
	PoolName          string `db:"PoolName"`
	Zone              string
	MaxSurge          string `db:"MaxSurge"`
	MaxUnavailable    string `db:"MaxUnavailable"`
	MachineClassName  string `db:"MachineClassName"`
	Labels            string
	Taints            string
	DeletionTimeStamp sql.NullInt64 `db:"DeletionTimeStamp"`
	Hash              string
}

func (r mcdRow) AsInfo() (mcdInfo gsc.MachineDeploymentInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = timeFromNano(r.DeletionTimeStamp.Int64)
	}
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	taints, err := taintsFromText(r.Taints)
	if err != nil {
		return
	}
	mcdInfo = gsc.MachineDeploymentInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: timeFromNano(r.CreationTimestamp),
			SnapshotTimestamp: timeFromNano(r.SnapshotTimestamp),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		Replicas:          r.Replicas,
		PoolName:          r.PoolName,
		Zone:              r.Zone,
		MaxSurge:          intstr.Parse(r.MaxSurge),
		MaxUnavailable:    intstr.Parse(r.MaxUnavailable),
		MachineClassName:  r.MachineClassName,
		Labels:            labels,
		Taints:            taints,
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}

type mccRow struct {
	RowID             int64 `db:"RowID"`
	CreationTimestamp int64 `db:"CreationTimestamp"`
	SnapshotTimestamp int64 `db:"SnapshotTimestamp"`
	Name              string
	Namespace         string
	InstanceType      string `db:"InstanceType"`
	PoolName          string `db:"PoolName"`
	Region            string
	Zone              string
	Labels            string
	Capacity          string
	DeletionTimeStamp sql.NullInt64 `db:"DeletionTimeStamp"`
	Hash              string
}

func (r mccRow) AsInfo() (mccInfo gsh.MachineClassInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = timeFromNano(r.DeletionTimeStamp.Int64)
	}
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	capacity, err := resourcesFromText(r.Capacity)
	if err != nil {
		return
	}
	mccInfo = gsh.MachineClassInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: timeFromNano(r.CreationTimestamp),
			SnapshotTimestamp: timeFromNano(r.SnapshotTimestamp),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		InstanceType:      r.InstanceType,
		PoolName:          r.PoolName,
		Region:            r.Region,
		Zone:              r.Zone,
		Labels:            labels,
		Capacity:          capacity,
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}

type nodeRow struct {
	RowID              int64 `db:"RowID"`
	Name               string
	Namespace          string
	CreationTimestamp  int64  `db:"CreationTimestamp"`
	SnapshotTimestamp  int64  `db:"SnapshotTimestamp"`
	ProviderID         string `db:"ProviderID"`
	AllocatableVolumes int    `db:"AllocatableVolumes"`
	Labels             string
	Taints             string
	Allocatable        string
	Capacity           string
	DeletionTimeStamp  sql.NullInt64
	Hash               string
}

func (r nodeRow) AsInfo() (nodeInfo gsc.NodeInfo, err error) {
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	taints, err := taintsFromText(r.Taints)
	if err != nil {
		return
	}
	allocatable, err := resourcesFromText(r.Allocatable)
	if err != nil {
		return
	}
	capacity, err := resourcesFromText(r.Capacity)
	if err != nil {
		return
	}
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = timeFromNano(r.DeletionTimeStamp.Int64)
	}
	nodeInfo = gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: timeFromNano(r.CreationTimestamp),
			SnapshotTimestamp: timeFromNano(r.SnapshotTimestamp),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		ProviderID:         r.ProviderID,
		AllocatableVolumes: r.AllocatableVolumes,
		Labels:             labels,
		Taints:             taints,
		Allocatable:        allocatable,
		Capacity:           capacity,
		DeletionTimestamp:  delTimeStamp,
		Hash:               r.Hash,
	}
	return
}

type podRow struct {
	RowID             int64 `db:"RowID"`
	CreationTimestamp int64 `db:"CreationTimestamp"`
	SnapshotTimestamp int64 `db:"SnapshotTimestamp"`
	Name              string
	Namespace         string
	UID               string `db:"UID"`
	NodeName          string `db:"NodeName"`
	NominatedNodeName string `db:"NominatedNodeName"`
	Labels            string
	Requests          string
	Spec              string
	ScheduleStatus    int `db:"ScheduleStatus"`
	DeletionTimeStamp sql.NullInt64
	Hash              string
}

func (r podRow) AsInfo() (podInfo gsc.PodInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = timeFromNano(r.DeletionTimeStamp.Int64)
	}
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	requests, err := resourcesFromText(r.Requests)
	if err != nil {
		return
	}
	spec, err := speccFromJson(r.Spec)
	if err != nil {
		return
	}
	podInfo = gsc.PodInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: timeFromNano(r.CreationTimestamp),
			SnapshotTimestamp: timeFromNano(r.SnapshotTimestamp),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		UID:               r.UID,
		NodeName:          r.NodeName,
		NominatedNodeName: r.NominatedNodeName,
		Labels:            labels,
		Requests:          requests,
		Spec:              spec,
		PodScheduleStatus: gsc.PodScheduleStatus(r.ScheduleStatus),
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}

type priorityClassRow struct {
	RowID             int64  `db:"RowID"`
	UID               string `db:"UID"`
	CreationTimestamp int64  `db:"CreationTimestamp"`
	SnapshotTimestamp int64  `db:"SnapshotTimestamp"`
	Name              string
	Value             int32
	GlobalDefault     bool   `db:"GlobalDefault"`
	PreemptionPolicy  string `db:"PreemptionPolicy"`
	Description       string
	Labels            string
	DeletionTimeStamp sql.NullInt64
	Hash              string
}

func (r priorityClassRow) AsInfo() (info gsc.PriorityClassInfo, err error) {
	var delTimeStamp *metav1.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = &metav1.Time{Time: timeFromNano(r.DeletionTimeStamp.Int64)}
	}
	var preemptionPolicy corev1.PreemptionPolicy
	if r.PreemptionPolicy != "" {
		preemptionPolicy = corev1.PreemptionPolicy(r.PreemptionPolicy)
	}
	labels, err := labelsFromText(r.Labels)
	priorityClass := schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:              r.Name,
			UID:               types.UID(r.UID),
			CreationTimestamp: metav1.Time{Time: timeFromNano(r.CreationTimestamp)},
			DeletionTimestamp: delTimeStamp,
			Labels:            labels,
		},
		Value:            r.Value,
		GlobalDefault:    r.GlobalDefault,
		Description:      r.Description,
		PreemptionPolicy: &preemptionPolicy,
	}
	pcInfo := gsc.PriorityClassInfo{
		RowID:             r.RowID,
		SnapshotTimestamp: timeFromNano(r.CreationTimestamp),
		PriorityClass:     priorityClass,
	}
	pcInfo.Hash = pcInfo.GetHash()
	return pcInfo, nil
}

type stateInfoRow struct {
	BeginTimestamp int64 `db:"BeginTimestamp"`
}

type caSettingsRow struct {
	RowID                         int64 `db:"RowID"`
	SnapshotTimestamp             int64 `db:"SnapshotTimestamp"`
	Expander                      string
	MaxNodeProvisionTime          int64 `db:"MaxNodeProvisionTime"`
	ScanInterval                  int64 `db:"ScanInterval"`
	MaxGracefulTerminationSeconds int   `db:"MaxGracefulTerminationSeconds"`
	NewPodScaleUpDelay            int64 `db:"NewPodScaleUpDelay"`
	MaxEmptyBulkDelete            int   `db:"MaxEmptyBulkDelete"`
	IgnoreDaemonSetUtilization    bool  `db:"IgnoreDaemonSetUtilization"`
	MaxNodesTotal                 int   `db:"MaxNodesTotal"`
	// NodeGroupsMinMax is the json of CASettingsInfo.NodeGroupsMinMax map
	NodeGroupsMinMax string `db:"NodeGroupsMinMax"`
	// Priorities is the value of the `priorities` key in the `cluster-autoscaler-priority-expander` config map.
	// See https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/expander/priority/readme.md#configuration
	Priorities string
	Hash       string //primary key
}

func timeFromNano(timestamp int64) time.Time {
	return time.Unix(0, timestamp).UTC()
}
func (r caSettingsRow) AsInfo() (caSettingsInfo gsc.CASettingsInfo, err error) {
	minMaxMap, err := minMaxMapFromText(r.NodeGroupsMinMax)
	if err != nil {
		return
	}
	caSettingsInfo = gsc.CASettingsInfo{
		SnapshotTimestamp:             timeFromNano(r.SnapshotTimestamp),
		Expander:                      r.Expander,
		MaxNodeProvisionTime:          time.Duration(r.MaxNodeProvisionTime * int64(time.Millisecond)),
		ScanInterval:                  time.Duration(r.ScanInterval * int64(time.Millisecond)),
		MaxGracefulTerminationSeconds: r.MaxGracefulTerminationSeconds,
		NewPodScaleUpDelay:            time.Duration(r.NewPodScaleUpDelay * int64(time.Millisecond)),
		MaxEmptyBulkDelete:            r.MaxEmptyBulkDelete,
		IgnoreDaemonSetUtilization:    r.IgnoreDaemonSetUtilization,
		MaxNodesTotal:                 r.MaxNodesTotal,
		NodeGroupsMinMax:              minMaxMap,
		Priorities:                    r.Priorities,
		Hash:                          r.Hash,
	}
	return
}
