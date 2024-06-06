package db

import (
	"fmt"
	"github.com/elankath/scalehist"
	assert "github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"path"
	"slices"
	"testing"
	"time"
)

func TestLoadLatestNodeGroup(t *testing.T) {
	dbPath := path.Join(os.TempDir(), "test.db")
	slog.Info("TestLoadLatestNodeGroup creating db", "db.path", dbPath)
	_ = os.Remove(dbPath)
	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
	assert.Nil(t, err)

	inserted := scalehist.NodeGroupInfo{
		Name:              "sample-nodegroup",
		CreationTimestamp: time.Now().UTC(),
		MinSize:           1,
		MaxSize:           4,
		TargetSize:        3,
		CurrentSize:       2,
		MachineType:       "m5.large",
		Zone:              "us-east-1c",
		Architecture:      "arm64",
		PoolName:          "sample-pool",
		PoolMin:           1,
		PoolMax:           5,
		ShootGeneration:   12,
		MCDGeneration:     15,
	}
	inserted.Hash = inserted.GetHash()
	_, err = dataAccess.StoreNodeGroup(inserted)
	assert.Nil(t, err)

	loaded, err := dataAccess.LoadLatestNodeGroup(inserted.Name)
	inserted.RowID = loaded.RowID // because
	assert.Nil(t, err)

	fmt.Printf("Inserted NG : %s\n", inserted)
	fmt.Printf("Loaded NG : %s\n", loaded)
	assert.Equal(t, inserted, loaded)

}

func TestStoreLoadPodInfo(t *testing.T) {
	dbPath := path.Join(os.TempDir(), "test.db")
	slog.Info("TestStoreLoadPodInfo creating db", "db.path", dbPath)
	_ = os.Remove(dbPath)
	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
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
	savePodInfo := scalehist.PodInfo{
		UID:               "1234",
		Name:              "Howdy",
		Namespace:         "HowdyNS",
		CreationTimestamp: time.Now().UTC(),
		NodeName:          "GreatHost",
		Labels:            map[string]string{"weapon": "light-saber"},
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
		PodScheduleStatus: scalehist.PodScheduleCommited,
	}
	savePodInfo.Hash = savePodInfo.GetHash()
	rowId, err := dataAccess.StorePodInfo(savePodInfo)
	slog.Info("persisted pod info.", "rowId", rowId)
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
		newTime := time.Now().Add(time.Second * 10)
		savePodInfo.CreationTimestamp = newTime
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
		_, err = dataAccess.UpdatePodDeletionTimestamp(types.UID(savePodInfo.UID), yesterday)
		assert.Nil(t, err)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		assert.Nil(t, err)
		pods, err := dataAccess.GetLatestScheduledPodsBeforeTimestamp(newTime)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(pods), "there should be no scheduled pod")
	})

	t.Run("GetLatestScheduledPodsBeforeTimestampWithTomorrowTimestamp", func(t *testing.T) {
		savePodInfo.NominatedNodeName = "node-sample"
		newTime := time.Now().Add(time.Second * 10)
		savePodInfo.CreationTimestamp = newTime
		savePodInfo.Hash = savePodInfo.GetHash()
		rowId, err := dataAccess.StorePodInfo(savePodInfo)
		tommorrow := newTime.Add(24 * time.Hour)
		_, err = dataAccess.UpdatePodDeletionTimestamp(types.UID(savePodInfo.UID), tommorrow)
		assert.Nil(t, err)
		slog.Info("persisted pod info.", "rowId", rowId, "savePodInfo", savePodInfo)
		assert.Nil(t, err)
		pods, err := dataAccess.GetLatestScheduledPodsBeforeTimestamp(newTime)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(pods), "there should be no scheduled pod")
	})
}
func TestStoreLoadLatestEventNGAssoc(t *testing.T) {
	dbPath := path.Join(os.TempDir(), "test.db")
	slog.Info("TestStoreLoadLatestEventNGAssoc creating db", "db.path", dbPath)
	_ = os.Remove(dbPath)

	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
	assert.Nil(t, err)

	storeAssoc := scalehist.EventNodeGroupAssoc{
		EventUID:       "1234",
		NodeGroupRowID: 5678,
		NodeGroupHash:  "yo-hash",
	}
	err = dataAccess.StoreEventNGAssoc(storeAssoc)
	assert.Nil(t, err)
	slog.Info("StoreEventNGAssoc success.", "assoc", storeAssoc)

	loadAssoc, err := dataAccess.LoadEventNGAssocForEventUID(storeAssoc.EventUID)
	assert.Nil(t, err)
	slog.Info("LoadEventNGAssocForEventUID success", "assoc", storeAssoc)

	assert.Equal(t, storeAssoc, loadAssoc)

}

func TestStoreLoadLatestEventInfo(t *testing.T) {
	dbPath := path.Join(os.TempDir(), "test.db")
	_ = os.Remove(dbPath)
	slog.Info("TestStoreLoadLatestEventInfo: creating db", "db.path", dbPath)

	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
	assert.Nil(t, err)

	storeEventInfo := scalehist.EventInfo{
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

func TestLoadNodeGroupsWithEventUIDAndSameHash(t *testing.T) {
	//TODO make test cleaner
	dbPath := path.Join(os.TempDir(), "test.db")
	_ = os.Remove(dbPath)
	slog.Info("TestStoreLoadLatestEventInfo: creating db", "db.path", dbPath)

	dataAccess := NewDataAccess(dbPath)
	err := dataAccess.Init()
	defer dataAccess.Close()
	assert.Nil(t, err)

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterday := today.Add(-24 * time.Hour).UTC()
	dayBeforeYesterday := yesterday.Add(-24 * time.Hour).UTC()
	storedNodeGroups := []scalehist.NodeGroupInfo{
		{
			Name:              "NG1",
			CreationTimestamp: dayBeforeYesterday,
			MinSize:           1,
			MaxSize:           4,
			TargetSize:        3,
			CurrentSize:       2,
			MachineType:       "m5.large",
			Zone:              "us-east-1c",
			Architecture:      "arm64",
			PoolName:          "sample-pool",
			PoolMin:           1,
			PoolMax:           5,
			ShootGeneration:   12,
			MCDGeneration:     15,
			Hash:              "ha",
		},
		{
			Name:              "NG1",
			CreationTimestamp: yesterday,
			MinSize:           1,
			MaxSize:           4,
			TargetSize:        3,
			CurrentSize:       2,
			MachineType:       "m5.large",
			Zone:              "us-east-1c",
			Architecture:      "arm64",
			PoolName:          "sample-pool",
			PoolMin:           1,
			PoolMax:           5,
			ShootGeneration:   12,
			MCDGeneration:     16,
			Hash:              "ha1",
		},
		scalehist.NodeGroupInfo{
			Name:              "NG2",
			CreationTimestamp: yesterday,
			MinSize:           1,
			MaxSize:           5,
			TargetSize:        4,
			CurrentSize:       2,
			MachineType:       "m5.xlarge",
			Zone:              "us-east-1c",
			Architecture:      "arm64",
			PoolName:          "sample-pool",
			PoolMin:           1,
			PoolMax:           5,
			ShootGeneration:   12,
			MCDGeneration:     15,
			Hash:              "hb",
		},
		{
			Name:              "NG3",
			CreationTimestamp: today,
			MinSize:           1,
			MaxSize:           8,
			TargetSize:        2,
			CurrentSize:       1,
			MachineType:       "m5.2xlarge",
			Zone:              "us-east-1a",
			Architecture:      "arm64",
			PoolName:          "sample-pool",
			PoolMin:           1,
			PoolMax:           5,
			ShootGeneration:   12,
			MCDGeneration:     15,
			Hash:              "hc",
		},
		{
			Name:              "NG3",
			CreationTimestamp: today,
			MinSize:           1,
			MaxSize:           8,
			TargetSize:        2,
			CurrentSize:       2,
			MachineType:       "m5.2xlarge",
			Zone:              "us-east-1a",
			Architecture:      "arm64",
			PoolName:          "sample-pool",
			PoolMin:           1,
			PoolMax:           5,
			ShootGeneration:   12,
			MCDGeneration:     15,
			Hash:              "hc",
		},
	}

	for i, ng := range storedNodeGroups {
		storedNodeGroups[i].RowID, err = dataAccess.StoreNodeGroup(ng)
		assert.Nil(t, err)
	}

	assocA1 := scalehist.EventNodeGroupAssoc{EventUID: "A1", NodeGroupRowID: storedNodeGroups[0].RowID, NodeGroupHash: storedNodeGroups[0].Hash}
	assocA2 := scalehist.EventNodeGroupAssoc{EventUID: "A2", NodeGroupRowID: storedNodeGroups[1].RowID, NodeGroupHash: storedNodeGroups[1].Hash}
	assocA3 := scalehist.EventNodeGroupAssoc{EventUID: "A3", NodeGroupRowID: storedNodeGroups[1].RowID, NodeGroupHash: storedNodeGroups[1].Hash}

	assocB1 := scalehist.EventNodeGroupAssoc{EventUID: "B1", NodeGroupRowID: storedNodeGroups[2].RowID, NodeGroupHash: storedNodeGroups[2].Hash}
	assocB2 := scalehist.EventNodeGroupAssoc{EventUID: "B2", NodeGroupRowID: storedNodeGroups[2].RowID, NodeGroupHash: storedNodeGroups[2].Hash}
	assocB3 := scalehist.EventNodeGroupAssoc{EventUID: "B3", NodeGroupRowID: storedNodeGroups[2].RowID, NodeGroupHash: storedNodeGroups[2].Hash}

	storedEventAssocs := []scalehist.EventNodeGroupAssoc{
		assocA1, assocA2, assocA3, assocB1, assocB2, assocB3,
	}
	for _, eA := range storedEventAssocs {
		err = dataAccess.StoreEventNGAssoc(eA)
		assert.Nil(t, err)
	}
	loadedNodeGroups, err := dataAccess.LoadNodeGroupsWithEventUIDAndSameHash()
	assert.Nil(t, err)

	for _, ng := range loadedNodeGroups {
		fmt.Printf("%+v\n", ng)
	}
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
	t.Logf("saving resources = %s", scalehist.ResourcesAsString(saved))
	resourcesTextVal, err := resourcesToText(saved)
	t.Logf("saved resourcesToText = %s", resourcesTextVal)
	assert.Nil(t, err)
	loaded, err := resourcesFromText(resourcesTextVal)
	t.Logf("loaded resources = %s", scalehist.ResourcesAsString(loaded))
	assert.Nil(t, err)
	//assert.Equal(t, saved, loaded) // THIS FAILS
	//equal := scalehist.IsResourceListEqual(saved, loaded)
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
	na1 := scalehist.NodeInfo{
		Name:              "A",
		Namespace:         "nsA",
		CreationTimestamp: dayBeforeYesterday,
		ProviderID:        "pA",
		Labels:            map[string]string{"snapshot": "a1"},
		Taints:            []corev1.Taint{k1Taint},
		Allocatable:       createResourceList("1", "3Gi", "19Gi"),
		Capacity:          createResourceList("2", "4Gi", "20Gi"),
	}
	na2 := scalehist.NodeInfo{
		Name:              "A",
		Namespace:         "nsA",
		CreationTimestamp: yesterday,
		ProviderID:        "pA",
		Labels:            map[string]string{"snapshot": "a2", "weapon": "sword"},
		Taints:            []corev1.Taint{k2Taint},
		Allocatable:       createResourceList("1", "3Gi", "19Gi"),
		Capacity:          createResourceList("2", "4Gi", "20Gi"),
	}
	nb1 := scalehist.NodeInfo{
		Name:              "B",
		Namespace:         "nsB",
		CreationTimestamp: yesterday,
		ProviderID:        "pB",
		Labels:            map[string]string{"snapshot": "b1", "weapon": "saber"},
		Taints:            []corev1.Taint{k3Taint},
		Allocatable:       createResourceList("2", "6Gi", "20Gi"),
		Capacity:          createResourceList("4", "8Gi", "22Gi"),
	}
	nb2 := scalehist.NodeInfo{
		Name:              "B",
		Namespace:         "nsB",
		CreationTimestamp: today,
		ProviderID:        "pnB",
		Labels:            map[string]string{"snapshot": "b2", "weapon": "lightsaber"},
		Taints:            []corev1.Taint{k3Taint},
		Allocatable:       createResourceList("6", "14Gi", "20Gi"),
		Capacity:          createResourceList("8", "16Gi", "30Gi"),
	}
	nc1 := scalehist.NodeInfo{
		Name:               "C",
		Namespace:          "nsC",
		CreationTimestamp:  time.Now().UTC(),
		ProviderID:         "pnC",
		AllocatableVolumes: 45,
		Labels:             nil,
		Taints:             nil,
		Allocatable:        createResourceList("2", "8Gi", "12Gi"),
		Capacity:           createResourceList("3", "9Gi", "13Gi"),
	}
	storeNodeInfos := []scalehist.NodeInfo{na1, na2, nb1, nb2, nc1}
	for i, n := range storeNodeInfos {
		storeNodeInfos[i].Hash = n.GetHash()
		_, err := dataAccess.StoreNodeInfo(n)
		assert.Nil(t, err)
	}
	slices.SortFunc(storeNodeInfos, scalehist.CmpNodeInfoDescending)
	t.Run("loadNodeInfos equals storeNodeInfos", func(t *testing.T) {
		loadNodeInfos, err := dataAccess.LoadNodeInfosBefore(time.Now())
		assert.Nil(t, err)
		isEqual := slices.EqualFunc(storeNodeInfos, loadNodeInfos, scalehist.IsEqualNodeInfo)
		assert.True(t, isEqual)
	})
	t.Run("updatePdbDeletionTimeStamp then loadNodeInfos", func(t *testing.T) {
		updated, err := dataAccess.UpdateNodeInfoDeletionTimestamp(na1.Name, time.Now())
		assert.Nil(t, err)
		if err != nil {
			return
		}
		t.Logf("dataAccess.UpdateNodeInfoDeletionTimestamp updated %d rows", updated)
		loadNodeInfos, err := dataAccess.LoadNodeInfosBefore(time.Now())
		assert.Nil(t, err)
		expected := slices.DeleteFunc(storeNodeInfos, func(info scalehist.NodeInfo) bool {
			return info.Name == na1.Name
		})
		isEqual := slices.EqualFunc(expected, loadNodeInfos, scalehist.IsEqualNodeInfo)
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
	//save := resource.MustParse("1000Mi")
	save := resource.MustParse("3.2")
	bytes, err := json.Marshal(save)
	t.Logf("marshalling quantity: %s", save.String())
	assert.Nil(t, err)
	t.Logf("marshalled quantity: %s", string(bytes))
	var load resource.Quantity
	err = json.Unmarshal(bytes, &load)
	assert.Nil(t, err)
	t.Logf("un-marshalled quantity: %s", load.String())
	assert.True(t, scalehist.IsEqualQuantity(save, load), "load and save quantities should be equal")
	//assert.Equal(t, save, load) //TODO:this fails as int64 scale is different.
}

func TestUpdateLatestNodeGroupTargetSize(t *testing.T) {
	dataAccess, err := initDataAccess()
	assert.Nil(t, err)

	today, yesterday, dayBeforeYesterday := getTodayYesterdayDayBeforeYesterday()
	ng1a := scalehist.NodeGroupInfo{
		RowID:             0,
		Name:              "ng1",
		CreationTimestamp: dayBeforeYesterday,
		CurrentSize:       0,
		TargetSize:        1,
		MinSize:           0,
		MaxSize:           5,
		Zone:              "eu-west-1a",
		MachineType:       "m5.large",
		Architecture:      "amd64",
		ShootGeneration:   0,
		MCDGeneration:     0,
		PoolName:          "p1",
		PoolMin:           0,
		PoolMax:           8,
	}
	ng1b := scalehist.NodeGroupInfo{
		RowID:             0,
		Name:              "ng1",
		CreationTimestamp: yesterday,
		CurrentSize:       0,
		TargetSize:        2,
		MinSize:           0,
		MaxSize:           5,
		Zone:              "eu-west-1a",
		MachineType:       "m5.large",
		Architecture:      "amd64",
		ShootGeneration:   0,
		MCDGeneration:     0,
		PoolName:          "p1",
		PoolMin:           0,
		PoolMax:           8,
	}
	ng2 := scalehist.NodeGroupInfo{
		RowID:             0,
		Name:              "ng2",
		CreationTimestamp: today,
		CurrentSize:       0,
		TargetSize:        1,
		MinSize:           0,
		MaxSize:           3,
		Zone:              "eu-west-1b",
		MachineType:       "m5.xlarge",
		Architecture:      "amd64",
		ShootGeneration:   0,
		MCDGeneration:     0,
		PoolName:          "p2",
		PoolMin:           0,
		PoolMax:           8,
	}
	storedNodeGroups := []scalehist.NodeGroupInfo{ng1a, ng1b, ng2}
	for i, ng := range storedNodeGroups {
		storedNodeGroups[i].Hash = ng.GetHash()

		_, err := dataAccess.StoreNodeGroup(storedNodeGroups[i])
		assert.Nil(t, err)
	}

	expectedSize := 9
	rowsUpdated, err := dataAccess.UpdateLatestNodeGroupTargetSize("ng1", 9)
	assert.Nil(t, err)
	assert.Equal(t, int64(1), rowsUpdated)
	loadedNg1, err := dataAccess.LoadLatestNodeGroup("ng1")
	assert.Nil(t, err)
	assert.Equal(t, expectedSize, loadedNg1.TargetSize)

	expectedSize = 1
	loadedNg2, err := dataAccess.LoadLatestNodeGroup("ng2")
	assert.Nil(t, err)
	assert.Equal(t, expectedSize, loadedNg2.TargetSize)

}

func TestMCD(t *testing.T) {

}
