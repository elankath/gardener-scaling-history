package db

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-history"
	assert "github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStoreLoadPodInfo(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	container := corev1.Container{
		Name:       "bingo",
		Image:      "bingo",
		Command:    []string{"/bin/bingo"},
		Args:       []string{"/bingo1/bingo2"},
		WorkingDir: "/bingowork",
		Env: []corev1.EnvVar{{
			Name:  "greeting",
			Value: "howdy",
		}},
	}
	now := time.Now().UTC()
	savePodInfo := gsc.PodInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			RowID:             0,
			CreationTimestamp: now,
			SnapshotTimestamp: now,
			Name:              "Howdy",
			Namespace:         "HowdyNS",
		},
		UID:      "1234",
		NodeName: "GreatHost",
		Labels:   map[string]string{"weapon": "light-saber"},
		Requests: map[corev1.ResourceName]resource.Quantity{
			corev1.ResourceCPU:     resource.MustParse("3.2"),
			corev1.ResourceMemory:  resource.MustParse("10Gi"),
			corev1.ResourceStorage: resource.MustParse("20Gi"),
		},
		Spec: corev1.PodSpec{
			Containers:    []corev1.Container{container},
			NodeName:      "node1",
			SchedulerName: "bin-packing",
		},
		PodScheduleStatus: gsc.PodScheduleCommited,
	}
	savePodInfo.Hash = savePodInfo.GetHash()
	rowId, err := dataAccess.StorePodInfo(savePodInfo)
	slog.Info("persisted pod info.", "RowID", rowId, "Hash", savePodInfo.Hash)
	assert.Nil(t, err)

	loadPodInfo, err := dataAccess.LoadLatestPodInfoWithName(savePodInfo.Name)
	assert.Nil(t, err)

	slog.Info("Inserted PodInfo", "podInfo", savePodInfo)
	slog.Info("Loaded PodInfo", "podInfo", loadPodInfo)

	//assert.Equal(t, savePodInfo, loadPodInfo, "savePodInfo must be equal to loadPodInfo")
	assert.Equal(t, loadPodInfo.Hash, loadPodInfo.GetHash(), "loadPodInfo.Hash() must equal  loadedPodInfo.GetHash()")
	assert.Equal(t, savePodInfo.Hash, loadPodInfo.GetHash(), "savedPodInfo.Hash() must be equal to loadedPodInfo.GetHash()")

	count, err := dataAccess.CountPodInfoWithSpecHash(loadPodInfo.UID, savePodInfo.Hash)
	assert.Nil(t, err)
	expected := 1
	assert.Equal(t, expected, count, "#%d pod info expected with hash: %s", expected, savePodInfo.Hash)
	bytes, _ := json.Marshal(savePodInfo)
	os.WriteFile("/tmp/podinfo.json", bytes, 0755)

	_, yesterday, _ := getTodayYesterdayDayBeforeYesterday()

	t.Run("GetLatestScheduledPodsBeforeTimestampWithNoDeletionTimeStamp", func(t *testing.T) {
		savePodInfo.NominatedNodeName = "node-sample"
		newTime := time.Now().Add(time.Second * 10).UTC()
		savePodInfo.SnapshotTimestamp = newTime
		savePodInfo.Hash = savePodInfo.GetHash()
		rowId, err := dataAccess.StorePodInfo(savePodInfo)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		assert.Nil(t, err)
		pods, err := dataAccess.GetLatestScheduledPodsBeforeTimestamp(newTime)
		assert.Nil(t, err)
		slog.Info("loaded pod info.", "loadPodInfo", pods[0])
		assert.Equal(t, 1, len(pods), "there should only be one latested pod with name %s", savePodInfo.Name)
		gotHash := pods[0].Hash
		assert.Equal(t, savePodInfo.Hash, gotHash)
	})

	t.Run("GetLatestScheduledPodsBeforeTimestampWithYesterdayDeletionTimestamp", func(t *testing.T) {
		savePodInfo.NominatedNodeName = "node-sample"
		newTime := time.Now().Add(time.Second * 10)
		savePodInfo.CreationTimestamp = newTime
		savePodInfo.Hash = savePodInfo.GetHash()
		rowId, err := dataAccess.StorePodInfo(savePodInfo)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		assert.Nil(t, err)
		_, err = dataAccess.UpdatePodDeletionTimestamp(types.UID(savePodInfo.UID), yesterday)
		assert.Nil(t, err)
		pods, err := dataAccess.GetLatestScheduledPodsBeforeTimestamp(newTime)
		assert.True(t, errors.Is(err, sql.ErrNoRows))
		assert.Equal(t, 0, len(pods), "there should be no scheduled pod")
	})

	t.Run("GetLatestScheduledPodsBeforeTimestampWithTomorrowTimestamp", func(t *testing.T) {
		savePodInfo.NominatedNodeName = "node-sample"
		snapshotTime := time.Now().Add(time.Second * 10).UTC()
		savePodInfo.SnapshotTimestamp = snapshotTime
		savePodInfo.Hash = savePodInfo.GetHash()
		rowId, err := dataAccess.StorePodInfo(savePodInfo)
		assert.Nil(t, err)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		tomorrow := snapshotTime.Add(24 * time.Hour).UTC()
		_, err = dataAccess.UpdatePodDeletionTimestamp(types.UID(savePodInfo.UID), tomorrow)
		assert.Nil(t, err)
		slog.Info("invoking GetLatestScheduledPodsBeforeTimestampWithTomorrowTimestamp", "timestamp", snapshotTime.UnixMilli())
		pods, err := dataAccess.GetLatestScheduledPodsBeforeTimestamp(snapshotTime)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(pods), "there should be one scheduled pod info")
	})

	t.Run("GetLatestPodInfosBeforeSnapshotTime", func(t *testing.T) {
		snapshotTime := time.Now().Add(time.Second * 10).UTC()
		savePodInfo.SnapshotTimestamp = snapshotTime
		savePodInfo.Hash = savePodInfo.GetHash()
		rowId, err := dataAccess.StorePodInfo(savePodInfo)
		assert.Nil(t, err)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		slog.Info("invoking GetLatestPodInfosBeforeSnapshotTime", "timestamp", snapshotTime.UnixMilli())
		pods, err := dataAccess.GetLatestPodInfosBeforeSnapshotTime(snapshotTime)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(pods), "there should be one scheduled pod info")
	})
}

func TestStoreLoadLatestEventInfo(t *testing.T) {
	dbPath := path.Join(os.TempDir(), "test.db")
	_ = os.Remove(dbPath)
	slog.Info("TestStoreLoadLatestEventInfo: creating db", "db.path", dbPath)

	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
	assert.Nil(t, err)

	storeEventInfo := gsc.EventInfo{
		UID:                     "uid1",
		EventTime:               time.Now().UTC(),
		ReportingController:     "controller",
		Reason:                  "reason",
		Message:                 "howdy do",
		InvolvedObjectKind:      "Deployment",
		InvolvedObjectName:      "yo-mama",
		InvolvedObjectNamespace: "my-namespace",
		InvolvedObjectUID:       "uid2",
	}
	err = dataAccess.StoreEventInfo(storeEventInfo)
	assert.Nil(t, err)
	slog.Info("StoreEventInfo success.", "event", storeEventInfo)

	loadAssoc, err := dataAccess.LoadEventInfoWithUID(storeEventInfo.UID)
	assert.Nil(t, err)
	slog.Info("LoadEventInfo success", "event", storeEventInfo)

	assert.Equal(t, storeEventInfo, loadAssoc)

}

func TestStoreLoadTriggerScaleUpEvents(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)
	defer dataAccess.Close()

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()

	e1 := gsc.EventInfo{
		UID:                     "uid1",
		EventTime:               dayBeforeYesterday,
		ReportingController:     "controller",
		Reason:                  "TriggeredScaleUp",
		Message:                 "event1",
		InvolvedObjectKind:      "Deployment",
		InvolvedObjectName:      "yo-mama",
		InvolvedObjectNamespace: "my-namespace",
		InvolvedObjectUID:       "objuid",
	}
	err = dataAccess.StoreEventInfo(e1)
	assert.Nil(t, err)
	slog.Info("StoreEventInfo success.", "event", e1)

	e2 := e1
	e2.UID = "uid2"
	e2.EventTime = yesterday
	e2.Message = "node2 launched"
	err = dataAccess.StoreEventInfo(e2)
	assert.Nil(t, err)
	slog.Info("StoreEventInfo success.", "event", e2)

	e3 := e2
	e3.UID = "uid3"
	e3.EventTime = today
	e3.Message = "node3 launched"
	err = dataAccess.StoreEventInfo(e3)
	assert.Nil(t, err)
	slog.Info("StoreEventInfo success.", "event", e2)

	loadedEvents, err := dataAccess.LoadTriggeredScaleUpEvents()
	assert.Nil(t, err)
	slog.Info("LoadTriggeredScaleUpEvents success", "loadedEvents", loadedEvents)
	assert.True(t, len(loadedEvents) == 3, "there should be 3 loadedEvents")
	assert.Equal(t, e1, loadedEvents[0])
	assert.Equal(t, e2, loadedEvents[1])
	assert.Equal(t, e3, loadedEvents[2])

}

func TestResourceListFromToText(t *testing.T) {
	saved := make(corev1.ResourceList)
	memory := resource.MustParse("5Gi")
	diskSize := resource.MustParse("5G")
	cpu := resource.MustParse("6.4")
	saved[corev1.ResourceCPU] = cpu
	fmt.Printf("milliCores = %v (%v)\n", cpu.MilliValue(), cpu.Format)
	saved[corev1.ResourceMemory] = memory
	saved[corev1.ResourceStorage] = diskSize
	t.Logf("saving resources = %s", gsc.ResourcesAsString(saved))
	resourcesTextVal, err := resourcesToText(saved)
	t.Logf("saved resourcesToText = %s", resourcesTextVal)
	assert.Nil(t, err)
	loaded, err := resourcesFromText(resourcesTextVal)
	t.Logf("loaded resources = %s", gsc.ResourcesAsString(loaded))
	assert.Nil(t, err)
	//assert.Equal(t, saved, loaded) // THIS FAILS
	//equal := recorder.IsResourceListEqual(saved, loaded)
	//assert.True(t, equal, "saved: %v expected to equal loaded: %v", saved, loaded)
}

func createResourceList(cpu, memory, disk string) (resources corev1.ResourceList) {
	resources = make(corev1.ResourceList)
	resources[corev1.ResourceCPU] = resource.MustParse(cpu)
	resources[corev1.ResourceMemory] = resource.MustParse(memory)
	resources[corev1.ResourceStorage] = resource.MustParse(disk)
	return
}

func TestStoreLoadNodeInfo(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)
	defer dataAccess.Close()

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()
	k1Taint := corev1.Taint{
		Key:       "k1",
		Value:     "v1",
		Effect:    corev1.TaintEffectNoSchedule,
		TimeAdded: &metav1.Time{dayBeforeYesterday},
	}
	k2Taint := corev1.Taint{
		Key:       "k2",
		Value:     "v2",
		Effect:    corev1.TaintEffectPreferNoSchedule,
		TimeAdded: &metav1.Time{yesterday},
	}
	k3Taint := corev1.Taint{
		Key:       "k3",
		Value:     "v3",
		Effect:    "Custom",
		TimeAdded: &metav1.Time{today},
	}
	a1 := gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: dayBeforeYesterday,
			SnapshotTimestamp: dayBeforeYesterday,
			Name:              "A1",
			Namespace:         "nsA",
		},
		ProviderID:  "a1",
		Labels:      map[string]string{"snapshot": "a1"},
		Taints:      []corev1.Taint{k1Taint},
		Allocatable: createResourceList("1", "3Gi", "19Gi"),
		Capacity:    createResourceList("2", "4Gi", "20Gi"),
	}
	a2 := gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: yesterday,
			SnapshotTimestamp: yesterday,
			Name:              "A2",
			Namespace:         "nsA",
		},
		ProviderID:  "a2",
		Labels:      map[string]string{"snapshot": "a2", "weapon": "sword"},
		Taints:      []corev1.Taint{k2Taint},
		Allocatable: createResourceList("1", "3Gi", "19Gi"),
		Capacity:    createResourceList("2", "4Gi", "20Gi"),
	}
	b1 := gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: yesterday,
			SnapshotTimestamp: yesterday,
			Name:              "B1",
			Namespace:         "nsB",
		},
		ProviderID:  "b1",
		Labels:      map[string]string{"snapshot": "b1", "weapon": "saber"},
		Taints:      []corev1.Taint{k3Taint},
		Allocatable: createResourceList("2", "6Gi", "20Gi"),
		Capacity:    createResourceList("4", "8Gi", "22Gi"),
	}
	b2 := gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: today,
			SnapshotTimestamp: today,
			Name:              "B2",
			Namespace:         "nsB",
		},
		ProviderID:  "b2",
		Labels:      map[string]string{"snapshot": "b2", "weapon": "lightsaber"},
		Taints:      []corev1.Taint{k3Taint},
		Allocatable: createResourceList("6", "14Gi", "20Gi"),
		Capacity:    createResourceList("8", "16Gi", "30Gi"),
	}

	now := time.Now().UTC()
	c1 := gsc.NodeInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: now,
			SnapshotTimestamp: now,
			Name:              "C1",
			Namespace:         "nsC",
		},
		ProviderID:         "c1",
		AllocatableVolumes: 45,
		Labels:             nil,
		Taints:             nil,
		Allocatable:        createResourceList("2", "8Gi", "12Gi"),
		Capacity:           createResourceList("3", "9Gi", "13Gi"),
	}
	storeNodeInfos := []gsc.NodeInfo{a1, a2, b1, b2, c1}
	for i, n := range storeNodeInfos {
		storeNodeInfos[i].Hash = n.GetHash()
		rowID, err := dataAccess.StoreNodeInfo(n)
		storeNodeInfos[i].RowID = rowID
		t.Logf("storedNode %q with RodID: %d", n.Name, rowID)
		assert.Nil(t, err)
	}
	slices.SortFunc(storeNodeInfos, gsc.CmpNodeInfoDescending)
	t.Run("loadNodeInfos equals storeNodeInfos", func(t *testing.T) {
		loadNodeInfos, err := dataAccess.LoadNodeInfosBefore(now)
		assert.Nil(t, err)
		slices.SortFunc(loadNodeInfos, gsc.CmpNodeInfoDescending)
		isEqual := slices.EqualFunc(storeNodeInfos, loadNodeInfos, gsc.IsEqualNodeInfo)
		assert.True(t, isEqual)
	})
}

func initDataAccess() (dataAccess *DataAccess, err error) {
	dbPath := path.Join(os.TempDir(), "test.db")
	slog.Info("TestLoadLatestNodeGroup creating db", "db.path", dbPath)
	_ = os.Remove(dbPath)
	dataAccess = NewDataAccess(dbPath)
	err = dataAccess.Init()
	return dataAccess, err
}

func getTodayYesterdayDayBeforeYesterday() (today, yesterday, dayBeforeYesterday time.Time) {
	now := time.Now()
	today = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterday = today.Add(-24 * time.Hour).UTC()
	dayBeforeYesterday = yesterday.Add(-24 * time.Hour).UTC()
	return
}

func TestCanonicalizeQuantity(t *testing.T) {
	// Create a resource.Quantity object
	//save := resource.MustParseQuantity("1000Mi")
	save := resource.MustParse("3.2")
	bytes, err := json.Marshal(save)
	t.Logf("marshalling quantity: %s", save.String())
	assert.Nil(t, err)
	t.Logf("marshalled quantity: %s", string(bytes))
	var load resource.Quantity
	err = json.Unmarshal(bytes, &load)
	assert.Nil(t, err)
	t.Logf("un-marshalled quantity: %s", load.String())
	assert.True(t, gsc.IsEqualQuantity(save, load), "load and save quantities should be equal")
	//assert.Equal(t, save, load) //TODO:this fails as int64 scale is different.
}

func TestStoreLoadMachineDeploymentInfos(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()
	m1 := gsc.MachineDeploymentInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: dayBeforeYesterday,
			SnapshotTimestamp: yesterday,
			Name:              "A",
			Namespace:         "nsA",
		},
		Replicas:         1,
		PoolName:         "p1",
		Zone:             "z1",
		MaxSurge:         intstr.Parse("30"),
		MaxUnavailable:   intstr.Parse("20%"),
		MachineClassName: "bingo",
		Labels: map[string]string{
			"greeting": "howdy",
			"weapon":   "light-saber",
		},
		Taints: []corev1.Taint{
			{
				Key:       "bingo",
				Value:     "tringo",
				Effect:    corev1.TaintEffectNoSchedule,
				TimeAdded: &metav1.Time{Time: today},
			},
		},
	}
	m1.Hash = m1.GetHash()
	t.Run("Simple single Store/Load", func(t *testing.T) {
		t.Logf("StoreMachineDeploymentInfo: %s", m1)
		rowID, err := dataAccess.StoreMachineDeploymentInfo(m1)
		assert.Nil(t, err)
		m1.RowID = rowID
		t.Logf("LoadMachineDeploymentInfosBefore: %d", today.UnixMilli())
		mcdInfos, err := dataAccess.LoadMachineDeploymentInfosBefore(today)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(mcdInfos), "only 1 mcdInfo should be present at this time")
		t.Logf("LoadMachineDeploymentInfosBefore returned:  %s", mcdInfos[0])
		assert.Equal(t, m1, mcdInfos[0])
	})

}

// TestStoreLoadWorkerPoolInfos has a TODO: should leverage a generified test of a load/store op
func TestStoreLoadWorkerPoolInfos(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()
	w1 := gsc.WorkerPoolInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: dayBeforeYesterday,
			SnapshotTimestamp: yesterday,
			Name:              "A",
			Namespace:         "nsA",
		},
		MachineType:    "m-small",
		Architecture:   "amd64",
		Minimum:        1,
		Maximum:        20,
		MaxSurge:       intstr.Parse("30"),
		MaxUnavailable: intstr.Parse("20%"),
		Zones:          []string{"us-east-1a", "us-west-2b"},
	}
	w1.Hash = w1.GetHash()
	t.Logf("StoreWorkerPoolInfo: %s", w1)
	rowID, err := dataAccess.StoreWorkerPoolInfo(w1)
	assert.Nil(t, err)
	w1.RowID = rowID

	w2 := w1
	w2.CreationTimestamp = yesterday
	w2.SnapshotTimestamp = today
	w2.Name = "B"
	w2.Minimum = 1
	w2.Maximum = 3
	w2.MachineType = "m5.xlarge"
	w2.Zones = []string{"eu-east-1b", "us-west-2c"}
	w2.Hash = w2.GetHash()
	rowID, err = dataAccess.StoreWorkerPoolInfo(w2)
	assert.Nil(t, err)
	w2.RowID = rowID

	t.Run("LoadWorkerPoolInfosBefore", func(t *testing.T) {
		t.Logf("LoadWorkerPoolInfosBefore: %d", today.UnixMilli())
		poolInfos, err := dataAccess.LoadWorkerPoolInfosBefore(today)
		assert.Nil(t, err)
		assert.Equal(t, 2, len(poolInfos), "2 WorkerPoolInfo should be present at this time")
		t.Logf("LoadWorkerPoolInfosBefore returned:  %s", poolInfos)
		slices.SortFunc(poolInfos, func(a, b gsc.WorkerPoolInfo) int {
			return strings.Compare(a.Name, b.Name)
		})
		assert.Equal(t, w1, poolInfos[0])
		assert.Equal(t, w2, poolInfos[1])

	})

	t.Run("LoadAllWorkerPoolInfoHashes", func(t *testing.T) {
		poolHashes, err := dataAccess.LoadAllWorkerPoolInfoHashes()
		assert.Nil(t, err)

		assert.Equal(t, 2, len(poolHashes), "only 2 WorkerPoolInfo hashes should be present at this time")
		t.Logf("LoadAllWorkerPoolInfoHashes returned:  %s", poolHashes)
	})

}

const ResourceGPU corev1.ResourceName = "gpu"

func TestStoreLoadMachineClassInfos(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()
	c1 := gsh.MachineClassInfo{
		SnapshotMeta: gsc.SnapshotMeta{
			CreationTimestamp: dayBeforeYesterday,
			SnapshotTimestamp: yesterday,
			Name:              "A",
			Namespace:         "nsA",
		},
		InstanceType: "m5.xlarge",
		PoolName:     "bingo-pool",
		Region:       "us-east1",
		Zone:         "us-east1-c",
		Labels: map[string]string{
			"greeting": "howdy",
			"weapon":   "light-saber",
		},
		Capacity: map[corev1.ResourceName]resource.Quantity{
			corev1.ResourceCPU:     gsc.MustParseQuantity("20.2"),
			corev1.ResourceMemory:  gsc.MustParseQuantity("16Gi"),
			corev1.ResourceStorage: gsc.MustParseQuantity("20Gi"),
			ResourceGPU:            gsc.MustParseQuantity("1"),
		},
	}
	c1.Hash = c1.GetHash()
	t.Logf("StoreMachineClassInfo: %s", c1)
	rowID, err := dataAccess.StoreMachineClassInfo(c1)
	assert.Nil(t, err)
	c1.RowID = rowID

	t.Run("Store/Load by name", func(t *testing.T) {
		t.Logf("LoadLatestMachineClassInfo for name: %s", c1.Name)
		loadInfo, err := dataAccess.LoadLatestMachineClassInfo(c1.Name)
		assert.Nil(t, err)
		t.Logf("LoadLatestMachineClassInfo returned:  %s", loadInfo)
		assert.Equal(t, c1, loadInfo)
	})

	t.Run("Store/Load Before", func(t *testing.T) {
		c2 := c1
		c2.InstanceType = "m5.large"
		c2.PoolName = "tringo-pool"
		c2.Region = "eu-west1"
		c2.Zone = "eu-west1-a"
		c2.CreationTimestamp = today
		c2.SnapshotTimestamp = today
		c2.Hash = c1.GetHash()
		t.Logf("StoreMachineClassInfo: %s", c2)
		rowID, err := dataAccess.StoreMachineClassInfo(c2)
		assert.Nil(t, err)
		c2.RowID = rowID
		t.Logf("LoadMachineClassInfosBefore: %d", yesterday.UnixMilli())
		poolInfos, err := dataAccess.LoadMachineClassInfosBefore(yesterday)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(poolInfos), "only 1 MachineClassInfo should be present at this time")

		t.Logf("LoadMachineClassInfosBefore returned:  %s", poolInfos[0])
		assert.Equal(t, c1, poolInfos[0])

	})
}

func TestLoadStorePriorityClassInfo(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	_, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()

	premptionPolicy := corev1.PreemptLowerPriority
	pc1 := gsc.PriorityClassInfo{
		SnapshotTimestamp: dayBeforeYesterday,
		PriorityClass: schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "howdy",
				UID:               "uid1",
				Generation:        0,
				CreationTimestamp: metav1.Time{Time: dayBeforeYesterday},
				Labels: map[string]string{
					"greeting": "howdy",
					"weapon":   "light-saber",
				},
			},
			Value:            123,
			GlobalDefault:    false,
			Description:      "bingo",
			PreemptionPolicy: &premptionPolicy,
		},
	}
	pc1.Hash = pc1.GetHash()
	t.Logf("StorePriorityClassInfo: %s", pc1)
	rowID, err := dataAccess.StorePriorityClassInfo(pc1)
	assert.Nil(t, err)
	pc1.RowID = rowID

	t.Logf("LoadLatestPriorityClassInfoBeforeSnapshotTime: %d", yesterday.UnixMilli())
	pcInfos, err := dataAccess.LoadLatestPriorityClassInfoBeforeSnapshotTime(yesterday)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(pcInfos), "only 1 PriorityClassInfo should be present at this time")
	t.Logf("Loaded PriorityClassInfo: %s", pcInfos[0])
	assert.Equal(t, pc1.Hash, pcInfos[0].Hash)

	t.Run("CountPCInfoWithSpecHash", func(t *testing.T) {
		count, err := dataAccess.CountPCInfoWithSpecHash(string(pc1.UID), pc1.Hash)
		assert.Nil(t, err)
		assert.Equal(t, 1, count)
	})

}

func TestStoreLoadEmptyNodeInfos(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	nodeInfos, err := dataAccess.LoadNodeInfosBefore(time.Now().UTC())
	assert.Nil(t, nodeInfos)
	assert.NotNil(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}
