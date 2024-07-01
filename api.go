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
}

type ClusterSnapshot struct {
	SnapshotTime     time.Time
	AutoscalerConfig gst.AutoScalerConfig
	PriorityClasses  []gst.PriorityClassInfo
	Pods             []gst.PodInfo
	Nodes            []gst.NodeInfo
}

type Scenario struct {
	ExistingNodes      []corev1.Node
	UnscheduledPods    []corev1.Pod
	ScaledUpNodeGroups map[string]int
	NominatedPods      []corev1.Pod
	ScheduledPods      []corev1.Pod
	ScaledUpNodes      []corev1.Node
}

type ReplayReport struct {
	StartTime time.Time
	Scenarios []Scenario
}

type Replayer interface {
	io.Closer
	Start(context.Context) error
	GetRecordedClusterSnapshot(time.Time) (ClusterSnapshot, error)
	GetParams() ReplayerParams
	Replay(context.Context) error
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
