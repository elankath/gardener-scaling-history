package gsh

import (
	"context"
	"github.com/elankath/gardener-scaling-types"
	"io"
	corev1 "k8s.io/api/core/v1"
	"time"
)

var ZoneLabels = []string{"topology.gke.io/zone", "topology.ebs.csi.aws.com/zone"}

// Recorder monitors the cluster denoted by given kubeconfig and records events and cluster data into cluster database
type Recorder interface {
	io.Closer
	Start(ctx context.Context) error
	//	GetRecordedClusterSnapshot(time time.Time) (ClusterSnapshot, error)
}

//Current ClusterInfo in gst -> ClusterAutoscalerConfig

type RecorderParams struct {
	Landscape           string
	ShootKubeConfigPath string
	ShootNameSpace      string
	SeedKubeConfigPath  string
	DBDir               string
	SchedulerName       string
}

type ReplayerParams struct {
	DBPath                       string
	ReportDir                    string
	VirtualAutoScalerConfigPath  string
	VirtualClusterKubeConfigPath string
	StabilizeInterval            time.Duration
	ReplayInterval               time.Duration
	TotalReplayTime              time.Duration
	RecurConfigUpdate            bool
}

type ClusterSnapshot struct {
	SnapshotTime     time.Time
	AutoscalerConfig gst.AutoScalerConfig
	WorkerPools      []gst.WorkerPoolInfo
	PriorityClasses  []gst.PriorityClassInfo
	Pods             []gst.PodInfo
	Nodes            []gst.NodeInfo
}

// TODO: OLD type commented, remove me later
//type Scenario struct {
//	BeginTime              time.Time
//	ExistingNodes          []gst.NodeInfo
//	PriorityClasses        []gst.PriorityClassInfo //TODO populate PC's
//	UnscheduledPods        []gst.PodInfo
//	ScaledUpNodeGroups     map[string]int
//	NominatedPods          []gst.PodInfo
//	ScheduledPods          []gst.PodInfo //pods mapped to ExistingNodes
//	ScaledUpNodes          []gst.NodeInfo
//	PendingUnscheduledPods []gst.PodInfo
//	WorkerPools            []gst.WorkerPoolInfo //TODO populate worker pools.
//}

type ScalingResult struct {
	ScaledUpNodeGroups     map[string]int
	ScaledUpNodes          []gst.NodeInfo
	PendingUnscheduledPods []gst.PodInfo
}

type Scenario struct {
	BeginTime       time.Time
	ClusterSnapshot ClusterSnapshot
	ScalingResult   ScalingResult
}

type ReplayReport struct {
	StartTime time.Time
	Scenarios []Scenario
	//	InitialClusterSnapshot gst.
}

type Replayer interface {
	io.Closer
	Start(context.Context) error
	GetRecordedClusterSnapshot(time.Time) (ClusterSnapshot, error)
	GetParams() ReplayerParams
	Replay(context.Context) error
	//input report - scenario report, output report-
	// mode1 of replayer is produce scneanrio reports off the recorded data - there is a /tmp/replayer-report.json
	// mode2 - recommender
	// recommender initializes replayer
	// recomender calls replayer.ReplayScenario.
	// replayer loads the scenario date from /tmp/replay-erreport.js and replays the scenario on kvcl (with recommender As CA)
	// it produces another ReplayReport and puts in another dir.
	//Then we report-comparer which will compare CA report1 and  Recommender report1
	// replayer is running in mode2
}

type MachineClassInfo struct {
	gst.SnapshotMeta

	// Instance type of the node belonging to nodeGroup
	InstanceType string

	// PoolName is the name of the gardener shoot worker pool that this machine class belongs to
	PoolName string

	// Region of the node belonging to nodeGroup
	Region string

	// Zone of the node that will be associated with this machine class
	Zone string

	// Labels is the machine class provider spec labels.
	Labels map[string]string

	// Capacity contains subfields to track all node resources required to scale nodegroup from zero
	Capacity corev1.ResourceList

	DeletionTimestamp time.Time
	Hash              string
}

type PodInfoKey struct {
	UID  string
	Name string
	Hash string
}

type PoolZone struct {
	PoolName string
	Zone     string
}
