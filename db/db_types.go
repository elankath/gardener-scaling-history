package db

import (
	"database/sql"
	gcr "github.com/elankath/gardener-cluster-recorder"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strings"
	"time"
)

type row[I any] interface {
	AsInfo() (I, error)
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

func (r workerPoolRow) AsInfo() (mcdInfo gcr.WorkerPoolInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
	}
	var zones []string
	if strings.TrimSpace(r.Zones) != "" {
		zones = strings.Split(r.Zones, " ")
	}
	mcdInfo = gcr.WorkerPoolInfo{
		SnapshotMeta: gcr.SnapshotMeta{
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
	MaxSurge          string        `db:"MaxSurge"`
	MaxUnavailable    string        `db:"MaxUnavailable"`
	MachineClassName  string        `db:"MachineClassName"`
	DeletionTimeStamp sql.NullInt64 `db:"DeletionTimeStamp"`
	Hash              string
}

func (r mcdRow) AsInfo() (mcdInfo gcr.MachineDeploymentInfo, err error) {
	var delTimeStamp time.Time
	if r.DeletionTimeStamp.Valid {
		delTimeStamp = time.UnixMilli(r.DeletionTimeStamp.Int64)
	}
	mcdInfo = gcr.MachineDeploymentInfo{
		SnapshotMeta: gcr.SnapshotMeta{
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

func (r nodeRow) AsInfo() (nodeInfo gcr.NodeInfo, err error) {
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
	nodeInfo = gcr.NodeInfo{
		SnapshotMeta: gcr.SnapshotMeta{
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

func (r podRow) AsInfo() (podInfo gcr.PodInfo, err error) {
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
	podInfo = gcr.PodInfo{
		SnapshotMeta: gcr.SnapshotMeta{
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
		PodScheduleStatus: gcr.PodScheduleStatus(r.ScheduleStatus),
		DeletionTimestamp: delTimeStamp,
		Hash:              r.Hash,
	}
	return
}
