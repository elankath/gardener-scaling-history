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
	//	GetClusterSnapshot(time time.Time) (ClusterSnapshot, error)
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

type ReporterParams struct {
	DBDir     string
	ReportDir string
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
