package db

import (
	"database/sql"
	"github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-types"
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

func (r workerPoolRow) AsInfo() (mcdInfo gst.WorkerPoolInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
	}
	var zones []string
	if strings.TrimSpace(r.Zones) != "" {
		zones = strings.Split(r.Zones, " ")
	}
	mcdInfo = gst.WorkerPoolInfo{
		SnapshotMeta: gst.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: time.UnixMilli(r.CreationTimestamp).UTC(),
			SnapshotTimestamp: time.UnixMilli(r.SnapshotTimestamp).UTC(),
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

func (r mcdRow) AsInfo() (mcdInfo gst.MachineDeploymentInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
	}
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	taints, err := taintsFromText(r.Taints)
	if err != nil {
		return
	}
	mcdInfo = gst.MachineDeploymentInfo{
		SnapshotMeta: gst.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: time.UnixMilli(r.CreationTimestamp).UTC(),
			SnapshotTimestamp: time.UnixMilli(r.SnapshotTimestamp).UTC(),
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
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
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
		SnapshotMeta: gst.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: time.UnixMilli(r.CreationTimestamp).UTC(),
			SnapshotTimestamp: time.UnixMilli(r.SnapshotTimestamp).UTC(),
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

func (r nodeRow) AsInfo() (nodeInfo gst.NodeInfo, err error) {
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
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
	}
	nodeInfo = gst.NodeInfo{
		SnapshotMeta: gst.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: time.UnixMilli(r.CreationTimestamp).UTC(),
			SnapshotTimestamp: time.UnixMilli(r.SnapshotTimestamp).UTC(),
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

func (r podRow) AsInfo() (podInfo gst.PodInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
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
	podInfo = gst.PodInfo{
		SnapshotMeta: gst.SnapshotMeta{
			RowID:             r.RowID,
			CreationTimestamp: time.UnixMilli(r.CreationTimestamp).UTC(),
			SnapshotTimestamp: time.UnixMilli(r.SnapshotTimestamp).UTC(),
			Name:              r.Name,
			Namespace:         r.Namespace,
		},
		UID:               r.UID,
		NodeName:          r.NodeName,
		NominatedNodeName: r.NominatedNodeName,
		Labels:            labels,
		Requests:          requests,
		Spec:              spec,
		PodScheduleStatus: gst.PodScheduleStatus(r.ScheduleStatus),
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}
