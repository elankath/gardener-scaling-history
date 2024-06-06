package scalehist

import (
	"context"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"time"
)

// Recorder monitors the cluster denoted by given kubeconfig and records events and cluster data into cluster database
type Recorder interface {
	io.Closer
	Start(ctx context.Context) error
}

type RecorderParams struct {
	ShootKubeConfigPath string
	ShootNameSpace      string
	SeedKubeConfigPath  string
	DBDir               string
	SchedulerName       string
}

type Analyzer interface {
	io.Closer
	Analyze(ctx context.Context) (Analysis, error)
}

type Reporter interface {
	GenerateTextReport(analysis Analysis) (reportPath string, err error)
	GenerateJsonReport(analysis Analysis) (reportPath string, err error)
}

type ReporterParams struct {
	DBDir     string
	ReportDir string
}

type Analysis struct {
	Name               string
	CoalesceInterval   string
	TolerationInterval string
	Scenarios          []Scenario
	//TODO: think of other useful fields.
}

type Scenario struct {
	StartTime                 time.Time
	EndTime                   time.Time
	SystemComponentRequests   corev1.ResourceList
	CriticalComponentRequests corev1.ResourceList
	CASettings                CASettingsInfo
	UnscheduledPods           []PodInfo
	NominatedPods             []PodInfo
	ScheduledPods             []PodInfo
	NodeGroups                []NodeGroupInfo
	ScaleUpEvents             []EventInfo
	Nodes                     []NodeInfo
}

type NodeGroupInfo struct {
	RowID             int64 `db:"RowID"` // give db tags only for mixed case fields
	Name              string
	CreationTimestamp time.Time `db:"CreationTimestamp"`
	CurrentSize       int       `db:"CurrentSize"`
	TargetSize        int       `db:"TargetSize"`
	MinSize           int       `db:"MinSize"`
	MaxSize           int       `db:"MaxSize"`
	Zone              string
	MachineType       string `db:"MachineType"`
	Architecture      string
	ShootGeneration   int64  `db:"ShootGeneration"`
	MCDGeneration     int64  `db:"MCDGeneration"`
	PoolName          string `db:"PoolName"`
	PoolMin           int    `db:"PoolMin"`
	PoolMax           int    `db:"PoolMax"`
	Hash              string
}

type WorkerPool struct {
	Name            string
	MachineType     string
	Architecture    string
	Minimum         int
	Maximum         int
	MaxSurge        intstr.IntOrString // TODO: persist as string if needed.
	MaxUnavailable  intstr.IntOrString // TODO: persist as string if needed.
	ShootGeneration int64
	Zones           []string
	//Volume TODO
}

type CASettingsInfo struct {
	Expander      string
	MaxNodesTotal int `db:"MaxNodesTotal"`
	// Priorities is the value of the `priorities` key in the `cluster-autoscaler-priority-expander` config map.
	// See https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/expander/priority/readme.md#configuration
	Priorities string
	Hash       string //primary key
}

type PodScheduleStatus int

const PodSchedulePending = -2
const PodScheduleNominated = -1
const PodUnscheduled = 0
const PodScheduleCommited = 1

type PodInfo struct {
	Name              string
	Namespace         string
	UID               string
	CreationTimestamp time.Time
	NodeName          string
	NominatedNodeName string
	Labels            map[string]string
	Requests          corev1.ResourceList
	Spec              corev1.PodSpec
	PodScheduleStatus PodScheduleStatus
	Hash              string
}

// NodeInfo represents snapshot information captured about an active k8s Node in the cluster at a particular moment in time.
// The snapshot time is captured in CreationTimestamp. A NodeInfo snapshot is only captured if there is a change in the properties
// excepting for DeletionTimestamp, in which case the DeletionTimestamp is only updated.
type NodeInfo struct {
	Name               string
	Namespace          string
	CreationTimestamp  time.Time
	ProviderID         string
	AllocatableVolumes int
	Labels             map[string]string
	Taints             []corev1.Taint
	Allocatable        corev1.ResourceList
	Capacity           corev1.ResourceList
	Hash               string
}

type PodInfoKey struct {
	UID  string
	Name string
	Hash string
}

type EventInfo struct {
	UID                     string    `db:"UID"`
	EventTime               time.Time `db:"EventTime"`
	ReportingController     string    `db:"ReportingController"`
	Reason                  string    `db:"Reason"`
	Message                 string    `db:"Message"`
	InvolvedObjectKind      string    `db:"InvolvedObjectKind"`
	InvolvedObjectName      string    `db:"InvolvedObjectName"`
	InvolvedObjectNamespace string    `db:"InvolvedObjectNamespace"`
	InvolvedObjectUID       string    `db:"InvolvedObjectUID"`
}

type EventNodeGroupAssoc struct {
	EventUID       string `db:"EventUID"`
	NodeGroupRowID int64  `db:"NodeGroupRowID"`
	NodeGroupHash  string `db:"NodeGroupHash"`
}

type EventCASettingsAssoc struct {
	EventUID       string `db:"EventUID"`
	CASettingsHash string `db:"CASettingsHash"`
}
