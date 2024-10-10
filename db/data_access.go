package db

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/blockloop/scan/v2"
	"github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	_ "github.com/glebarez/go-sqlite"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"slices"
	"strings"
	"time"
)

type DataAccess struct {
	io.Closer
	dataDBPath                                           string
	dataDB                                               *sql.DB
	insertWorkerPoolInfo                                 *sql.Stmt
	selectWorkerPoolInfosBefore                          *sql.Stmt
	selectAllWorkerPoolInfoHashes                        *sql.Stmt
	insertMCDInfo                                        *sql.Stmt
	updateMCDInfoDeletionTimeStamp                       *sql.Stmt
	selectMCDInfoHash                                    *sql.Stmt
	selectLatestMCDInfoBefore                            *sql.Stmt
	selectLatestMCDInfo                                  *sql.Stmt
	insertMCCInfo                                        *sql.Stmt
	updateMCCInfoDeletionTimeStamp                       *sql.Stmt
	selectMCCInfoHash                                    *sql.Stmt
	selectLatestMCCInfoBefore                            *sql.Stmt
	selectLatestMCCInfo                                  *sql.Stmt
	insertEvent                                          *sql.Stmt
	insertNodeInfo                                       *sql.Stmt
	updateNodeInfoDeletionTimeStamp                      *sql.Stmt
	insertCSINodeInfo                                    *sql.Stmt
	updateCSINodeInfoDeletionTimeStamp                   *sql.Stmt
	insertPodInfo                                        *sql.Stmt
	insertPriorityClassInfo                              *sql.Stmt
	insertPDB                                            *sql.Stmt
	updatePodDeletionTimeStamp                           *sql.Stmt
	updatePdbDeletionTimeStamp                           *sql.Stmt
	selectLatestPodInfoWithName                          *sql.Stmt
	selectPodCountWithUIDAndHash                         *sql.Stmt
	selectEventWithUID                                   *sql.Stmt
	selectAllEvents                                      *sql.Stmt
	selectUnscheduledPodsBeforeSnapshotTimestamp         *sql.Stmt
	selectScheduledPodsBeforeSnapshotTimestamp           *sql.Stmt
	selectPriorityClassInfoWithUIDAndHash                *sql.Stmt
	selectLatestPodInfosBetweenSnapshotTimestamps        *sql.Stmt
	selectLatestPriorityClassInfoBeforeSnapshotTimestamp *sql.Stmt
	selectNodeInfosBefore                                *sql.Stmt
	selectCSINodeInfosBefore                             *sql.Stmt
	selectNodeCountWithNameAndHash                       *sql.Stmt
	selectLatestCASettingsInfo                           *sql.Stmt
	insertCASettingsInfo                                 *sql.Stmt
	selectCADeploymentByHash                             *sql.Stmt
	selectLatestNodesBeforeAndNotDeleted                 *sql.Stmt
	selectLatestCASettingsInfoBefore                     *sql.Stmt
	selectInitialRecorderStateInfo                       *sql.Stmt
	selectTriggerScaleUpEvents                           *sql.Stmt
}

func NewDataAccess(dataDBPath string) *DataAccess {
	access := &DataAccess{
		dataDBPath: dataDBPath,
	}
	return access
}

func (d *DataAccess) Init() error {
	db, err := sql.Open("sqlite", d.dataDBPath)
	if err != nil {
		return fmt.Errorf("cannot open db: %w", err)
	}
	// From:https://kerkour.com/sqlite-for-servers
	_, err = db.Exec("PRAGMA journal_mode = WAL")
	if err != nil {
		return err
	}
	_, err = db.Exec("PRAGMA busy_timeout = 5000")
	if err != nil {
		return err
	}
	_, err = db.Exec("PRAGMA synchronous = NORMAL")
	if err != nil {
		return err
	}
	_, err = db.Exec("PRAGMA cache_size = 20000")
	if err != nil {
		return err
	}
	_, err = db.Exec("PRAGMA temp_store = memory")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	d.dataDB = db
	err = d.createSchema()
	if err != nil {
		return fmt.Errorf("error creating db schema: %w", err)
	}
	err = d.prepareStatements()
	if err != nil {
		return fmt.Errorf("error preparing statements: %w", err)
	}
	return nil
}

func (d *DataAccess) Close() error {
	if d.dataDB == nil {
		return nil
	}
	slog.Info("stopping data db", "dataDBPath", d.dataDBPath)
	err := d.dataDB.Close()
	if err != nil {
		slog.Warn("cannot close data db", "error", err)
		return err
	}
	d.dataDB = nil
	return nil
}

func (d *DataAccess) InsertRecorderStartTime(startTime time.Time) error {
	_, err := d.dataDB.Exec(InsertRecorderStateInfo, startTime.UTC().UnixMicro())
	if err != nil {
		return fmt.Errorf("cannot execute the InsertRecorderStateInfo statement: %w", err)
	}
	return nil
}

func (d *DataAccess) prepareStatements() (err error) {
	db := d.dataDB
	d.insertWorkerPoolInfo, err = db.Prepare(InsertWorkerPoolInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertWorkerPoolInfo statement: %w", err)
	}

	d.selectWorkerPoolInfosBefore, err = db.Prepare(SelectWorkerPoolInfoBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectWorkerPoolInfosBefore statement: %w", err)
	}

	d.selectAllWorkerPoolInfoHashes, err = db.Prepare(SelectAllWorkerPoolInfoHashes)
	if err != nil {
		return fmt.Errorf("cannot prepare selectAllWorkerPoolInfoHashes statement: %w", err)
	}

	d.insertNodeInfo, err = db.Prepare(InsertNodeInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertNodeInfo statement: %w", err)
	}
	d.updateNodeInfoDeletionTimeStamp, err = db.Prepare(UpdateNodeInfoDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updateNodeInfoDeletionTimeStamp: %w", err)
	}

	d.selectNodeInfosBefore, err = db.Prepare(SelectNodeInfoBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectNodeInfosBefore statement: %w", err)
	}

	d.insertCSINodeInfo, err = db.Prepare(InsertCSINodeInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertCSINodeInfo statement: %w", err)
	}

	d.updateCSINodeInfoDeletionTimeStamp, err = db.Prepare(UpdateCSINodeInfoDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updateCSINodeInfoDeletionTimeStamp: %w", err)
	}

	d.selectCSINodeInfosBefore, err = db.Prepare(SelectCSINodeInfoBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectCSINodeInfosBefore statement: %w", err)
	}

	pdbInsertStmt, err := db.Prepare("INSERT INTO pdb_info(uid,name,generation,creationTimestamp,minAvailable,maxUnAvailable,spec) VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return fmt.Errorf("cannot prepare pdb insert statement : %w", err)
	}
	d.insertPDB = pdbInsertStmt

	d.updatePdbDeletionTimeStamp, err = db.Prepare("UPDATE pdb_info SET DeletionTimeStamp=? WHERE uid=?")
	if err != nil {
		return fmt.Errorf("cannot prepare updatePdbDeletionTimeStamp: %w", err)
	}

	d.insertMCDInfo, err = db.Prepare(InsertMCDInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertMCDInfo: %w", err)
	}

	d.updateMCDInfoDeletionTimeStamp, err = db.Prepare(UpdateMCDInfoDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updateMCDInfoDeletionTimeStamp: %w", err)
	}

	d.selectLatestMCDInfoBefore, err = db.Prepare(SelectLatestMCDInfoBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestMCDInfoBefore statement: %w", err)
	}

	d.selectLatestMCDInfo, err = db.Prepare(SelectLatestMCDInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestMCDInfo: %w", err)
	}

	d.selectMCDInfoHash, err = db.Prepare(SelectMCDInfoHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectMCDInfoHash: %w", err)
	}

	d.insertMCCInfo, err = db.Prepare(InsertMCCInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertMCCInfo: %w", err)
	}

	d.updateMCCInfoDeletionTimeStamp, err = db.Prepare(UpdateMCCInfoDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updateMCCInfoDeletionTimeStamp: %w", err)
	}

	d.selectLatestMCCInfoBefore, err = db.Prepare(SelectLatestMCCInfoBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestMCCInfoBefore statement: %w", err)
	}

	d.selectLatestMCCInfo, err = db.Prepare(SelectLatestMCCInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestMCCInfo: %w", err)
	}

	d.selectMCCInfoHash, err = db.Prepare(SelectMCCInfoHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectMCCInfoHash: %w", err)
	}

	d.selectLatestPodInfoWithName, err = db.Prepare("SELECT * FROM pod_info WHERE Name=? ORDER BY RowID DESC LIMIT 1")
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestPodInfoWithName: %w", err)
	}
	d.insertEvent, err = db.Prepare(InsertEvent)
	if err != nil {
		return fmt.Errorf("cannot prepare events insert statement: %w", err)
	}
	d.insertPodInfo, err = db.Prepare(InsertPodInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare pod insert statement: %w", err)
	}
	d.insertPriorityClassInfo, err = db.Prepare(InsertPriorityClassInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare InsertPriorityClassInfo statement: %w", err)
	}

	//TODO: must create indexes
	d.selectPodCountWithUIDAndHash, err = db.Prepare(SelectPodCountWithUIDAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectPodCountWithUIDAndHash: %w", err)
	}

	d.updatePodDeletionTimeStamp, err = db.Prepare(UpdatePodDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updatePodDeletionTimeStamp: %w", err)
	}

	d.selectEventWithUID, err = db.Prepare("SELECT * from event_info where UID = ?")

	d.selectAllEvents, err = db.Prepare("SELECT * from event_info ORDER BY EventTime")
	if err != nil {
		return fmt.Errorf("cannot prepare selectAllEvents statement: %w", err)
	}

	d.selectUnscheduledPodsBeforeSnapshotTimestamp, err = db.Prepare(SelectUnscheduledPodsBeforeSnapshotTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectUnscheduledPodsBeforeSnapshotTimestamp statement: %w", err)
	}

	d.selectScheduledPodsBeforeSnapshotTimestamp, err = db.Prepare(SelectLatestScheduledPodsBeforeSnapshotTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectScheduledPodsBeforeSnapshotTimestamp statement: %w", err)
	}

	d.selectLatestPodInfosBetweenSnapshotTimestamps, err = db.Prepare(SelectLatestPodsBetweenSnapshotTimestamps)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestPodInfosBetweenSnapshotTimestamps statement: %w", err)
	}

	d.selectLatestPriorityClassInfoBeforeSnapshotTimestamp, err = db.Prepare(SelectLatestPriorityClassInfoBeforeSnapshotTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestPriorityClassInfoBeforeSnapshotTimestamp statement: %w", err)
	}

	d.selectPodCountWithUIDAndHash, err = db.Prepare(SelectPodCountWithUIDAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectPodCountWithUIDAndHash: %w", err)
	}

	d.selectPriorityClassInfoWithUIDAndHash, err = db.Prepare(SelectPriorityClassInfoCountWithUIDAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectPriorityClassInfoWithUIDAndHash: %w", err)
	}

	d.selectNodeCountWithNameAndHash, err = db.Prepare(SelectNodeCountWithNameAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectNodeCountWithNameAndHash: %w", err)
	}

	d.selectCADeploymentByHash, err = db.Prepare(SelectCADeploymentByHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectCADeploymentByHash: %w", err)
	}

	d.selectLatestCASettingsInfo, err = db.Prepare(SelectLatestCASettingsInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestCASettingsInfo")
	}

	d.selectLatestCASettingsInfoBefore, err = db.Prepare(SelectLatestCASettingsBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestCASettingsInfoBefore")
	}

	d.insertCASettingsInfo, err = db.Prepare(InsertCASettingsInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare insertCASettingsInfo statement")
	}

	d.selectLatestNodesBeforeAndNotDeleted, err = db.Prepare(SelectLatestNodesBeforeAndNotDeleted)
	if err != nil {
		return fmt.Errorf("cannot prepare ")
	}

	d.selectInitialRecorderStateInfo, err = d.dataDB.Prepare(SelectInitialRecorderStateInfo)
	if err != nil {
		return
	}
	d.selectTriggerScaleUpEvents, err = d.dataDB.Prepare(SelectTriggerScaleUpEvents)
	if err != nil {
		return
	}
	return err
}

func (d *DataAccess) createSchema() error {
	var db = d.dataDB
	var err error
	var result sql.Result

	result, err = db.Exec(CreateRecorderStateInfo)
	if err != nil {
		return fmt.Errorf("cannot create recorder_state_info table: %w", err)
	}
	slog.Info("successfully created recorder_state_info table", "result", result)

	result, err = db.Exec(CreateWorkerPoolInfo)
	if err != nil {
		return fmt.Errorf("cannot create worker_pool_info table: %w", err)
	}
	slog.Info("successfully created worker_pool_info table", "result", result)

	result, err = db.Exec(CreateMCDInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create mcd_info table: %w", err)
	}
	slog.Info("successfully created mcd_info table", "result", result)

	result, err = db.Exec(CreateMCCInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create mcc_info table: %w", err)
	}
	slog.Info("successfully created mcc_info table", "result", result)

	result, err = db.Exec(CreateEventInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create event_info table: %w", err)
	}

	slog.Info("successfully created event_info table", "result", result)

	//result, err = db.Exec(CreateNodeGroupInfoTable)SelectLatestPodsBetweenSnapshotTimestamps
	//if err != nil {
	//	return fmt.Errorf("cannot create nodegroup_info table: %w", err)
	//}
	//slog.Info("successfully created nodegroup_info table", "result", result)

	result, err = db.Exec(CreateNodeInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create node_info table : %w", err)
	}
	slog.Info("successfully created node_info table", "result", result)

	result, err = db.Exec(CreateCSINodeTable)
	if err != nil {
		return fmt.Errorf("cannot create csi_node_info table : %w", err)
	}
	slog.Info("successfully created csi_node_info table", "result", result)

	result, err = db.Exec(CreatePodInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create pod_info table: %w", err)
	}
	slog.Info("successfully created pod_info table", "result", result)

	result, err = db.Exec(CreatePriorityClassInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create pc_info table: %w", err)
	}
	slog.Info("successfully created pc_info table", "result", result)

	result, err = db.Exec(`CREATE TABLE IF NOT EXISTS pdb_info(
    							id INTEGER PRIMARY KEY AUTOINCREMENT,
    							uid TEXT,
    							name TEXT,
    							generation INT,
    							creationTimestamp DATETIME,
    							deletionTimestamp DATETIME,
    							minAvailable TEXT,
    							maxUnAvailable TEXT,
    							spec TEXT)`) // TODO: maxUnAvailable -> maxUnavailable
	if err != nil {
		return fmt.Errorf("cannot create pdb_info table: %w", err)
	}
	slog.Info("successfully created pdb_info table", "result", result)

	result, err = db.Exec(CreateCASettingsInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create ca_settings_info table: %w", err)
	}
	slog.Info("successfully created the ca_settings_info table")

	return nil
}

func (d *DataAccess) CountPCInfoWithSpecHash(uid, hash string) (int, error) {
	var count sql.NullInt32
	err := d.selectPriorityClassInfoWithUIDAndHash.QueryRow(uid, hash).Scan(&count)
	if count.Valid {
		return int(count.Int32), nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil
		}
	}
	return -1, err
}

func (d *DataAccess) CountPodInfoWithSpecHash(uid, hash string) (int, error) {
	var count sql.NullInt32
	err := d.selectPodCountWithUIDAndHash.QueryRow(uid, hash).Scan(&count)
	if count.Valid {
		return int(count.Int32), nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil
		}
	}
	return -1, err
}

func (d *DataAccess) CountNodeInfoWithHash(name, hash string) (int, error) {
	var count sql.NullInt32
	err := d.selectNodeCountWithNameAndHash.QueryRow(name, hash).Scan(&count)
	if count.Valid {
		return int(count.Int32), nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil
		}
	}
	return -1, err
}

func updateDeletionTimestamp(stmt *sql.Stmt, name string, deletionTimestamp time.Time) (updated int64, err error) {
	result, err := stmt.Exec(deletionTimestamp.UTC().UnixMicro(), name)
	if err != nil {
		return -1, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return -1, err
	}
	return updated, err
}

func (d *DataAccess) UpdatePodDeletionTimestamp(podUID types.UID, deletionTimestamp time.Time) (updated int64, err error) {
	return updateDeletionTimestamp(d.updatePodDeletionTimeStamp, string(podUID), deletionTimestamp)
}

func (d *DataAccess) UpdateNodeInfoDeletionTimestamp(name string, deletionTimestamp time.Time) (updated int64, err error) {
	return updateDeletionTimestamp(d.updateNodeInfoDeletionTimeStamp, name, deletionTimestamp)
}
func (d *DataAccess) UpdateCSINodeRowDeletionTimestamp(name string, deletionTimestamp time.Time) (updated int64, err error) {
	return updateDeletionTimestamp(d.updateCSINodeInfoDeletionTimeStamp, name, deletionTimestamp)
}

func (d *DataAccess) UpdateMCDInfoDeletionTimestamp(name string, deletionTimestamp time.Time) (updated int64, err error) {
	return updateDeletionTimestamp(d.updateMCDInfoDeletionTimeStamp, name, deletionTimestamp)
}

func (d *DataAccess) UpdateMCCInfoDeletionTimestamp(name string, deletionTimestamp time.Time) (updated int64, err error) {
	return updateDeletionTimestamp(d.updateMCCInfoDeletionTimeStamp, name, deletionTimestamp)
}

func (d *DataAccess) StoreEventInfo(event gsc.EventInfo) error {
	//eventsStmt, err := db.Prepare("INSERT INTO event_info(UID, EventTime, ReportingController, Reason, Message, InvolvedObjectKind, InvolvedObjectName, InvolvedObjectNamespace, InvolvedObjectUID) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)")
	_, err := d.insertEvent.Exec(
		event.UID,
		event.EventTime.UTC(),
		event.ReportingController,
		event.Reason,
		event.Message,
		event.InvolvedObjectKind,
		event.InvolvedObjectName,
		event.InvolvedObjectNamespace,
		event.InvolvedObjectUID,
	)
	return err
}

func (d *DataAccess) GetMachineDeploymentInfoHash(name string) (string, error) {
	return getHash(d.selectMCDInfoHash, name)
}

func (d *DataAccess) GetMachineClassInfoHash(name string) (string, error) {
	return getHash(d.selectMCCInfoHash, name)
}

func (d *DataAccess) StoreMachineDeploymentInfo(m gsc.MachineDeploymentInfo) (rowID int64, err error) {
	if m.Hash == "" {
		m.Hash = m.GetHash()
	}
	labels, err := labelsToText(m.Labels)
	if err != nil {
		return -1, err
	}
	taints, err := taintsToText(m.Taints)
	if err != nil {
		return -1, err
	}
	result, err := d.insertMCDInfo.Exec(
		m.CreationTimestamp.UTC().UnixMicro(),
		m.SnapshotTimestamp.UTC().UnixMicro(),
		m.Name,
		m.Namespace,
		m.Replicas,
		m.PoolName,
		m.Zone,
		m.MaxSurge.String(),
		m.MaxUnavailable.String(),
		m.MachineClassName,
		labels,
		taints,
		m.Hash)
	if err != nil {
		slog.Error("cannot insert MachineDeploymentInfo in the mcd_info table", "error", err)
		return
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		slog.Error("cannot retrieve rowID for MachineDeploymentInfo from the mcd_info table", "error", err, "name", m.Name)
		return
	}
	slog.Info("StoreMachineDeploymentInfo successful.", "Name", m.Name,
		"RowID", rowID,
		"Replicas", m.Replicas,
		"Hash", m.Hash,
	)
	return
}

func (d *DataAccess) StoreMachineClassInfo(m gsh.MachineClassInfo) (rowID int64, err error) {
	if m.Hash == "" {
		m.Hash = m.GetHash()
	}
	labels, err := labelsToText(m.Labels)
	if err != nil {
		return -1, err
	}
	capacity, err := resourcesToText(m.Capacity)
	if err != nil {
		return -1, err
	}
	result, err := d.insertMCCInfo.Exec(
		m.CreationTimestamp.UTC().UnixMicro(),
		m.SnapshotTimestamp.UTC().UnixMicro(),
		m.Name,
		m.Namespace,
		m.InstanceType,
		m.PoolName,
		m.Region,
		m.Zone,
		labels,
		capacity,
		m.Hash)
	if err != nil {
		slog.Error("cannot insert MachineClassInfo in the mcc_info table", "error", err)
		return
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		slog.Error("cannot retrieve rowID for MachineClassInfo from the mcc_info table", "error", err, "name", m.Name)
		return
	}
	slog.Info("StoreMachineClassInfo successful.", "Name", m.Name,
		"RowID", rowID,
		"InstanceType", m.InstanceType,
		"Region", m.Region,
		"Capacity", m.Capacity,
		"Hash", m.Hash,
	)
	return
}

func (d *DataAccess) StoreWorkerPoolInfo(w gsc.WorkerPoolInfo) (rowID int64, err error) {
	if w.Hash == "" {
		w.Hash = w.GetHash()
	}
	result, err := d.insertWorkerPoolInfo.Exec(
		w.CreationTimestamp.UTC().UnixMicro(),
		w.SnapshotTimestamp.UTC().UnixMicro(),
		w.Name,
		w.Namespace,
		w.MachineType,
		w.Architecture,
		w.Minimum,
		w.Maximum,
		w.MaxSurge.String(),
		w.MaxUnavailable.String(),
		strings.Join(w.Zones, " "),
		w.Hash)
	if err != nil {
		slog.Error("cannot insert WorkerPoolInfo in the worker_pool_info table", "error", err, "workerPoolInfo", workerPoolRow{})
		return
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		slog.Error("cannot retrieve rowID for WorkerPoolInfo from the worker_pool_info table", "error", err, "name", w.Name)
		return
	}
	slog.Info("StoreWorkerPoolInfo successful.",
		"RowID", rowID,
		"Name", w.Name,
		"Minimum", w.Minimum,
		"Maximum", w.Maximum,
		"Hash", w.Hash,
	)
	return
}

func (d *DataAccess) LoadAllWorkerPoolInfoHashes() (map[string]string, error) {
	poolHashes := make(map[string]string)
	nameHashTimes, err := queryRows[hashRow](d.selectAllWorkerPoolInfoHashes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return poolHashes, nil //empty table is not an error
		}
		return nil, fmt.Errorf("LoadAllWorkerPoolInfoHashes could not scan rows: %w", err)
	}
	for _, v := range nameHashTimes {
		_, ok := poolHashes[v.Name]
		if ok {
			continue
		}
		poolHashes[v.Name] = v.Hash
	}
	return poolHashes, nil
}

func (d *DataAccess) LoadWorkerPoolInfosBefore(snapshotTimestamp time.Time) ([]gsc.WorkerPoolInfo, error) {
	workerPoolInfos, err := queryAndMapToInfos[gsc.WorkerPoolInfo, workerPoolRow](d.selectWorkerPoolInfosBefore, snapshotTimestamp)
	if err != nil {
		return nil, fmt.Errorf("LoadWorkerPoolInfosBefore could not scan rows: %w", err)
	}
	return workerPoolInfos, nil
}

func (d *DataAccess) LoadMachineDeploymentInfosBefore(snapshotTimestamp time.Time) ([]gsc.MachineDeploymentInfo, error) {
	mcdInfos, err := queryAndMapToInfos[gsc.MachineDeploymentInfo, mcdRow](d.selectLatestMCDInfoBefore, snapshotTimestamp)
	if err != nil {
		return nil, fmt.Errorf("LoadMachineDeploymentInfosBefore could not scan rows: %w", err)
	}
	return mcdInfos, nil
}

func (d *DataAccess) LoadLatestMachineClassInfo(name string) (mccInfo gsh.MachineClassInfo, err error) {
	return queryAndMapToInfo[gsh.MachineClassInfo, mccRow](d.selectLatestMCCInfo, name)
}

func (d *DataAccess) LoadMachineClassInfosBefore(snapshotTimestamp time.Time) ([]gsh.MachineClassInfo, error) {
	mccInfos, err := queryAndMapToInfos[gsh.MachineClassInfo, mccRow](d.selectLatestMCCInfoBefore, snapshotTimestamp)
	if err != nil {
		return nil, fmt.Errorf("LoadMachineClassInfosBefore could not scan rows: %w", err)
	}
	return mccInfos, nil
}

func (d *DataAccess) LoadLatestMachineDeploymentInfo(name string) (mcdInfo gsc.MachineDeploymentInfo, err error) {
	return queryAndMapToInfo[gsc.MachineDeploymentInfo, mcdRow](d.selectLatestMCDInfo, name)
}

func (d *DataAccess) LoadEventInfoWithUID(eventUID string) (eventInfo gsc.EventInfo, err error) {
	rows, err := d.selectEventWithUID.Query(eventUID)
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Row(&eventInfo, rows) //TODO: wrap err with msg and return
	return
}

// LoadAllEvents TODO: move me to generics
func (d *DataAccess) LoadAllEvents() (events []gsc.EventInfo, err error) {
	rows, err := d.selectAllEvents.Query()
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Rows(&events, rows)
	return
}

func (d *DataAccess) LoadTriggeredScaleUpEvents() (events []gsc.EventInfo, err error) {
	rows, err := d.selectTriggerScaleUpEvents.Query()
	if err != nil {
		return
	}
	err = scan.Rows(&events, rows)
	slices.SortFunc(events, gsc.CompareEventsByEventTime)
	return
}

func (d *DataAccess) LoadLatestPodInfoWithName(podName string) (podInfo gsc.PodInfo, err error) {
	return queryAndMapToInfo[gsc.PodInfo, podRow](d.selectLatestPodInfoWithName, podName)
}

func (d *DataAccess) GetLatestUnscheduledPodsBeforeTimestamp(timeStamp time.Time) (podInfos []gsc.PodInfo, err error) {
	return queryAndMapToInfos[gsc.PodInfo, podRow](d.selectUnscheduledPodsBeforeSnapshotTimestamp, timeStamp, timeStamp)
}

func (d *DataAccess) GetLatestPodInfosBetweenSnapshotTimestamp(t1, t2 time.Time) (pods []gsc.PodInfo, err error) {
	return queryAndMapToInfos[gsc.PodInfo, podRow](d.selectLatestPodInfosBetweenSnapshotTimestamps, t1, t2, t2)
}

func (d *DataAccess) GetLatestScheduledPodsBeforeTimestamp(timestamp time.Time) (pods []gsc.PodInfo, err error) {
	slog.Info("GetLatestScheduledPodsBeforeTimestamp: selectScheduledPodsBeforeSnapshotTimestamp", "timestamp", timestamp.UTC().UnixMicro())
	return queryAndMapToInfos[gsc.PodInfo, podRow](d.selectScheduledPodsBeforeSnapshotTimestamp, timestamp, timestamp)
}

func (d *DataAccess) LoadLatestPriorityClassInfoBeforeSnapshotTime(snapshotTime time.Time) (pcInfos []gsc.PriorityClassInfo, err error) {
	return queryAndMapToInfos[gsc.PriorityClassInfo, priorityClassRow](d.selectLatestPriorityClassInfoBeforeSnapshotTimestamp, snapshotTime, snapshotTime)
}

func (d *DataAccess) LoadCASettingsBefore(timestamp time.Time) (caSettings gsc.CASettingsInfo, err error) {
	return queryAndMapToInfo[gsc.CASettingsInfo, caSettingsRow](d.selectLatestCASettingsInfoBefore, timestamp)
}

func (d *DataAccess) LoadLatestCASettingsInfo() (gsc.CASettingsInfo, error) {
	return queryAndMapToInfo[gsc.CASettingsInfo, caSettingsRow](d.selectLatestCASettingsInfo)
}

// GetCADeploymentWithHash has a  TODO: move me to generics
func (d *DataAccess) GetCADeploymentWithHash(Hash string) (caDeployment *gsc.CASettingsInfo, err error) {
	rows, err := d.selectLatestCASettingsInfo.Query(Hash)
	if err != nil {
		return
	}
	var caDeployments []gsc.CASettingsInfo
	err = scan.Rows(&caDeployments, rows)
	if err != nil {
		return nil, err
	}
	if len(caDeployments) == 0 {
		return nil, nil
	}
	caDeployment = &caDeployments[0]
	return
}

func (d *DataAccess) StorePodInfo(podInfo gsc.PodInfo) (int64, error) {
	if podInfo.Hash == "" {
		podInfo.Hash = podInfo.GetHash()
	}
	labels, err := labelsToText(podInfo.Labels)
	if err != nil {
		return -1, err
	}
	requests, err := resourcesToText(podInfo.Requests)
	if err != nil {
		return -1, err
	}
	podSpec, err := specToJson(podInfo.Spec)
	if err != nil {
		return -1, err
	}
	result, err := d.insertPodInfo.Exec(
		podInfo.CreationTimestamp.UTC().UnixMicro(),
		podInfo.SnapshotTimestamp.UTC().UnixMicro(),
		podInfo.Name,
		podInfo.Namespace,
		podInfo.UID,
		podInfo.NodeName,
		podInfo.NominatedNodeName,
		podInfo.PodPhase,
		labels,
		requests,
		podSpec,
		podInfo.PodScheduleStatus,
		podInfo.Hash)
	if err != nil {
		return -1, fmt.Errorf("could not persist podinfo %s: %w", podInfo, err)
	}
	slog.Debug("stored row into pod_info.", "pod.Name", podInfo.Name, "pod.Namespace", podInfo.Namespace,
		"pod.CreationTimestamp", podInfo.CreationTimestamp, "pod.Hash", podInfo.Hash)
	return result.LastInsertId()
}

func (d *DataAccess) StorePriorityClassInfo(pcInfo gsc.PriorityClassInfo) (int64, error) {
	if pcInfo.Hash == "" {
		pcInfo.Hash = pcInfo.GetHash()
	}
	labels, err := labelsToText(pcInfo.Labels)
	result, err := d.insertPriorityClassInfo.Exec(
		pcInfo.CreationTimestamp.UTC().UnixMicro(),
		pcInfo.SnapshotTimestamp.UTC().UnixMicro(),
		pcInfo.Name,
		pcInfo.UID,
		pcInfo.Value,
		pcInfo.GlobalDefault,
		*pcInfo.PreemptionPolicy,
		pcInfo.Description,
		labels,
		pcInfo.Hash)
	if err != nil {
		return -1, fmt.Errorf("could not persist PriorityClassInfo %s: %w", pcInfo, err)
	}
	slog.Info("stored row into pc_info.",
		"Name", pcInfo.Name,
		"Value", pcInfo.Value,
		"PreemptionPolicy", pcInfo.PreemptionPolicy,
		"Hash", pcInfo.Hash)
	return result.LastInsertId()
}

func (d *DataAccess) StoreNodeInfo(n gsc.NodeInfo) (rowID int64, err error) {
	if n.Hash == "" {
		n.Hash = n.GetHash()
	}
	// Removing this label as it just takes useless space: "node.machine.sapcloud.io/last-applied-anno-labels-taints"
	delete(n.Labels, "node.machine.sapcloud.io/last-applied-anno-labels-taints")
	labelsText, err := labelsToText(n.Labels)
	if err != nil {
		return
	}
	taintsText, err := taintsToText(n.Taints)
	if err != nil {
		return
	}
	allocatableText, err := resourcesToText(n.Allocatable)
	if err != nil {
		return
	}
	capacityText, err := resourcesToText(n.Capacity)
	if err != nil {
		return
	}
	result, err := d.insertNodeInfo.Exec(
		n.CreationTimestamp.UTC().UnixMicro(),
		n.SnapshotTimestamp.UTC().UnixMicro(),
		n.Name,
		n.Namespace,
		n.ProviderID,
		n.AllocatableVolumes,
		labelsText,
		taintsText,
		allocatableText,
		capacityText,
		n.Hash)
	if err != nil {
		slog.Error("cannot insert node_info in the node_info table", "error", err, "node", n)
		return -1, err
	}
	slog.Debug("inserted new row into the node_info table", "node.Name", n.Name)
	return result.LastInsertId()
}

func (d *DataAccess) StoreCSINodeRow(cn CSINodeRow) (rowID int64, err error) {
	result, err := d.insertCSINodeInfo.Exec(
		cn.CreationTimestamp,
		cn.SnapshotTimestamp,
		cn.Name,
		cn.Namespace,
		cn.AllocatableVolumes)
	if err != nil {
		slog.Error("cannot insert CSINodeRow in the csi_node_info table", "error", err, "csiNode", cn)
		return -1, err
	}
	slog.Debug("inserted new row into the csi_node_info table", "Name", cn.Name)
	return result.LastInsertId()
}

func (d *DataAccess) LoadCSINodeRowsBefore(snapshotTimestamp time.Time) ([]CSINodeRow, error) {
	//nodeInfos, err := queryAndMapToInfos[gsc.NodeInfo, nodeRow](d.selectNodeInfosBefore, snapshotTimestamp.UTC().UnixMicro(), snapshotTimestamp.UTC().UnixMicro())
	adjustedTimeParam := adjustParam(snapshotTimestamp)
	rowObjs, err := queryRows[CSINodeRow](d.selectCSINodeInfosBefore, adjustedTimeParam, adjustedTimeParam)
	if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("LoadCSINodeRowsBefore could not scan rows: %w", err)
	}
	return rowObjs, nil
}

func (d *DataAccess) LoadNodeInfosBefore(snapshotTimestamp time.Time) ([]gsc.NodeInfo, error) {
	nodeInfos, err := queryAndMapToInfos[gsc.NodeInfo, nodeRow](d.selectNodeInfosBefore, snapshotTimestamp.UTC().UnixMicro(), snapshotTimestamp.UTC().UnixMicro())
	if err != nil {
		return nil, fmt.Errorf("LoadNodeInfosBefore could not scan rows: %w", err)
	}
	return nodeInfos, nil
}

func (d *DataAccess) StoreCASettingsInfo(caSettings gsc.CASettingsInfo) (int64, error) {
	minMaxMapText, err := minMaxMapToText(caSettings.NodeGroupsMinMax)
	if err != nil {
		return -1, fmt.Errorf("cannot create row for CASettings: %w", err)
	}
	result, err := d.insertCASettingsInfo.Exec(
		caSettings.SnapshotTimestamp.UTC().UnixMicro(),
		caSettings.Expander,
		caSettings.ScanInterval.Milliseconds(),
		caSettings.MaxNodeProvisionTime.Milliseconds(),
		caSettings.MaxGracefulTerminationSeconds,
		caSettings.NewPodScaleUpDelay.Milliseconds(),
		caSettings.MaxEmptyBulkDelete,
		caSettings.IgnoreDaemonSetUtilization,
		caSettings.MaxNodesTotal,
		minMaxMapText,
		caSettings.Priorities,
		caSettings.Hash)
	if err != nil {
		return -1, err
	}
	return result.LastInsertId()
}

func (d *DataAccess) GetLatestNodesBeforeAndNotDeleted(timestamp time.Time) ([]gsc.NodeInfo, error) {
	nodeInfos, err := queryAndMapToInfos[gsc.NodeInfo, nodeRow](d.selectLatestNodesBeforeAndNotDeleted, timestamp)
	if err != nil {
		return nil, fmt.Errorf("GetLatestNodesBeforeAndNotDeleted could not scan rows: %w", err)
	}
	return nodeInfos, nil
}

func (d *DataAccess) GetInitialRecorderStartTime() (startTime time.Time, err error) {
	rows, err := queryRows[stateInfoRow](d.selectInitialRecorderStateInfo)
	if err != nil {
		return
	}
	return timeFromMicro(rows[0].BeginTimestamp), nil
}

func labelsToText(valMap map[string]string) (textVal string, err error) {
	if len(valMap) == 0 {
		return "", nil
	}
	bytes, err := json.Marshal(valMap)
	if err != nil {
		err = fmt.Errorf("cannot serialize labels %q due to: %w", valMap, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func resourcesToText(resources corev1.ResourceList) (textVal string, err error) {
	if len(resources) == 0 {
		return "", nil
	}
	bytes, err := json.Marshal(resources)
	if err != nil {
		err = fmt.Errorf("cannot serialize resources %v due to: %w", resources, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func tolerationsToText(tolerations []corev1.Toleration) (textVal string, err error) {
	if len(tolerations) == 0 {
		return "", nil
	}
	bytes, err := json.Marshal(tolerations)
	if err != nil {
		err = fmt.Errorf("cannot serialize tolerations %v due to: %w", tolerations, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func tscToText(tsc []corev1.TopologySpreadConstraint) (textVal string, err error) {
	if len(tsc) == 0 {
		return "", nil
	}
	bytes, err := json.Marshal(tsc)
	if err != nil {
		err = fmt.Errorf("cannot serialize TopologySpreadConstraints %v due to: %w", tsc, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func taintsToText(taints []corev1.Taint) (textVal string, err error) {
	if len(taints) == 0 {
		return "", nil
	}
	bytes, err := json.Marshal(taints)
	if err != nil {
		err = fmt.Errorf("cannot serialize taints %q due to: %w", taints, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func minMaxMapFromText(textValue string) (minMaxMap map[string]gsc.MinMax, err error) {
	if strings.TrimSpace(textValue) == "" {
		err = fmt.Errorf("loaded NodeGroupsMinMax is empty")
		return
	}
	err = json.Unmarshal([]byte(textValue), &minMaxMap)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize MinMaxMap %q due to: %w", textValue, err)
	}
	return
}

func minMaxMapToText(minMaxMap map[string]gsc.MinMax) (textVal string, err error) {
	if len(minMaxMap) == 0 {
		err = fmt.Errorf("minMaxMap is empty which is an invalid state")
		return
	}
	bytes, err := json.Marshal(minMaxMap)
	if err != nil {
		err = fmt.Errorf("cannot serialize minMaxMap %q due to: %w", minMaxMap, err)
	} else {
		textVal = string(bytes)
	}
	return
}

func labelsFromText(textValue string) (labels map[string]string, err error) {
	if strings.TrimSpace(textValue) == "" {
		return nil, nil
	}
	err = json.Unmarshal([]byte(textValue), &labels)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize labels %q due to: %w", textValue, err)
	}
	return
}

func tolerationsFromText(textValue string) (tolerations []corev1.Toleration, err error) {
	if strings.TrimSpace(textValue) == "" {
		return nil, nil
	}
	err = json.Unmarshal([]byte(textValue), &tolerations)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize tolerations %q due to: %w", textValue, err)
	}
	return
}

func tscFromText(textValue string) (tsc []corev1.TopologySpreadConstraint, err error) {
	if strings.TrimSpace(textValue) == "" {
		return nil, nil
	}
	err = json.Unmarshal([]byte(textValue), &tsc)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize TopologySpreadConstraints %q due to: %w", textValue, err)
	}
	return
}

func specToJson(podSpec corev1.PodSpec) (textVal string, err error) {
	bytes, err := json.Marshal(podSpec)
	if err != nil {
		err = fmt.Errorf("cannot serialize podSpec %q due to: %w", podSpec.String(), err)
	} else {
		textVal = string(bytes)
	}
	return
}
func speccFromJson(jsonVal string) (podSpec corev1.PodSpec, err error) {
	if strings.TrimSpace(jsonVal) == "" {
		return
	}
	err = json.Unmarshal([]byte(jsonVal), &podSpec)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize podSpec %q due to: %w", jsonVal, err)
	}
	return
}

func taintsFromText(textValue string) (taints []corev1.Taint, err error) {
	if strings.TrimSpace(textValue) == "" {
		return nil, nil
	}
	err = json.Unmarshal([]byte(textValue), &taints)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize taints %q due to: %w", textValue, err)
	}
	for i, t := range taints {
		if t.TimeAdded != nil {
			t.TimeAdded = &metav1.Time{Time: t.TimeAdded.UTC()}
		}
		taints[i] = t
	}
	return
}

func resourcesFromText(textValue string) (resources corev1.ResourceList, err error) {
	if strings.TrimSpace(textValue) == "" {
		return nil, nil
	}
	err = json.Unmarshal([]byte(textValue), &resources)
	if err != nil {
		err = fmt.Errorf("cannot de-serialize resources %q due to: %w", textValue, err)
	}
	return
}

func getHash(selectHashStmt *sql.Stmt, name string) (string, error) {
	row := selectHashStmt.QueryRow(name)
	var hash sql.NullString
	err := row.Scan(&hash)
	if hash.Valid {
		return hash.String, nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return "", nil

}

// queryAndMapToInfo executes the given prepared stmt with the given params and scans the db rows into rowObjs of specific generic type T.
// It is expected that field names of T are mapped to column names of the query via blockloop/scan `db` annotation
// if there are no rows returns sql.ErrNoRows as an error. Caller should check this.
func queryRows[T any](stmt *sql.Stmt, params ...any) (rowObjs []T, err error) {
	var rows *sql.Rows

	var adjustedParams = make([]any, len(params))
	for i, p := range params {
		adjustedParams[i] = adjustParam(p)
	}

	if len(params) > 0 {
		rows, err = stmt.Query(adjustedParams...)
	} else {
		rows, err = stmt.Query()
	}

	if err != nil {
		return
	}
	err = scan.Rows(&rowObjs, rows)
	if err != nil {
		return
	}
	if len(rowObjs) == 0 {
		return nil, sql.ErrNoRows
	}
	return
}

// queryAndMapToInfo executes the given prepared stmt with the given params and maps the rows to infoObjs which is a []I slice
func queryAndMapToInfos[I any, T row[I]](stmt *sql.Stmt, params ...any) (infoObjs []I, err error) {
	rowObjs, err := queryRows[T](stmt, params...)
	if err != nil {
		//if errors.Is(err, sql.ErrNoRows) {
		//	return nil, nil //empty result is not an error
		//}
		return nil, err
	}
	for _, r := range rowObjs {
		infoObj, err := r.AsInfo()
		if err != nil {
			return nil, err
		}
		infoObjs = append(infoObjs, infoObj)
	}
	return
}

func adjustParam(p any) any {
	if t, ok := p.(time.Time); ok {
		return t.UTC().UnixMicro()
	}
	return p
}

// queryAndMapToInfo executes the given prepared stmt with the given params and maps the first row to a single infoObj of type I
func queryAndMapToInfo[I any, T row[I]](stmt *sql.Stmt, param ...any) (infoObj I, err error) {
	rows, err := stmt.Query(param...)
	if err != nil {
		return
	}
	var rowObj T
	err = scan.Row(&rowObj, rows) //TODO: wrap err with msg and return
	if err != nil {
		err = fmt.Errorf("could not find rows for statement: %w", err)
		return
	}
	infoObj, err = rowObj.AsInfo()
	if err != nil {
		return
	}
	return infoObj, nil
}
