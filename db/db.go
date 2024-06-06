package db

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/blockloop/scan/v2"
	"github.com/elankath/gardener-scalehist"
	_ "github.com/glebarez/go-sqlite"
	"github.com/samber/lo"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"slices"
	"strings"
	"time"
)

type DataAccess struct {
	io.Closer
	dataDBPath                            string
	dataDB                                *sql.DB
	insertNodeGroup                       *sql.Stmt
	updateNodeGroupDeletionTimeStamp      *sql.Stmt
	insertEvent                           *sql.Stmt
	insertEventNGAssoc                    *sql.Stmt
	insertNodeInfo                        *sql.Stmt
	updateNodeInfoDeletionTimeStamp       *sql.Stmt
	insertPodInfo                         *sql.Stmt
	insertPDB                             *sql.Stmt
	updatePodDeletionTimeStamp            *sql.Stmt
	updatePdbDeletionTimeStamp            *sql.Stmt
	selectEventNGAssocForEventUID         *sql.Stmt
	selectMaxPDBGeneration                *sql.Stmt
	selectNodeGroupHash                   *sql.Stmt
	selectLatestNodeGroup                 *sql.Stmt
	selectLatestPodInfoWithName           *sql.Stmt
	selectPodCountWithUIDAndHash          *sql.Stmt
	selectMaxNodeGroupGeneration          *sql.Stmt
	selectMaxShootGen                     *sql.Stmt
	selectEventWithUID                    *sql.Stmt
	selectAllEvents                       *sql.Stmt
	selectUnscheduledPodsBeforeTimestamp  *sql.Stmt
	selectScheduledPodsBeforeTimestamp    *sql.Stmt
	selectLatestPodsBeforeTimestamp       *sql.Stmt
	selectNodeGroupsBefore                *sql.Stmt
	selectNodeGroupWithEventIDAndSameHash *sql.Stmt
	selectAllActiveNodeGroupsDesc         *sql.Stmt
	selectNodeInfosBefore                 *sql.Stmt
	selectNodeCountWithNameAndHash        *sql.Stmt
	selectLatestCADeployment              *sql.Stmt
	insertCADeployment                    *sql.Stmt
	selectCADeploymentByHash              *sql.Stmt
	insertEventCASettingsAssoc            *sql.Stmt
	selectEventCAAssocWithEventUID        *sql.Stmt
	selectLatestNodesBeforeAndNotDeleted  *sql.Stmt
	updateLatestNodeGroupSize             *sql.Stmt
}

type NodeGroupWithEventUID struct {
	scalehist.NodeGroupInfo
	EventUID string `db:"EventUID"`
}

type nodeRow struct {
	Name               string
	Namespace          string
	ProviderID         string `db:"ProviderID"`
	AllocatableVolumes int    `db:"AllocatableVolumes"`
	CreationTimestamp  int64  `db:"CreationTimestamp"`
	Labels             string
	Taints             string
	Allocatable        string
	Capacity           string
	Hash               string
}

type podRow struct {
	RowID             int    `db:"RowID"`
	UID               string `db:"UID"`
	Name              string
	Namespace         string
	CreationTimestamp int64  `db:"CreationTimestamp"`
	NodeName          string `db:"NodeName"`
	NominatedNodeName string `db:"NominatedNodeName"`
	Labels            string
	Requests          string
	Spec              string
	ScheduleStatus    int `db:"ScheduleStatus"`
	Hash              string
}

type nodeGroupRow struct {
	RowID             int64 `db:"RowID"` // give db tags only for mixed case fields
	Name              string
	CreationTimestamp int64 `db:"CreationTimestamp"`
	CurrentSize       int   `db:"CurrentSize"`
	TargetSize        int   `db:"TargetSize"`
	MinSize           int   `db:"MinSize"`
	MaxSize           int   `db:"MaxSize"`
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

func (d *DataAccess) prepareStatements() (err error) {
	db := d.dataDB
	d.insertEventNGAssoc, err = db.Prepare(InsertEventNodeGroupAssoc)
	if err != nil {
		return fmt.Errorf("cannot create insertEventNGAssoc statement: %w", err)
	}
	d.selectEventNGAssocForEventUID, err = db.Prepare("SELECT * FROM event_nodegroup_assoc WHERE EventUID =? LIMIT 1")
	if err != nil {
		return fmt.Errorf("cannot prepare selectEventNGAssocForEventUID: %w", err)
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

	pdbInsertStmt, err := db.Prepare("INSERT INTO pdb_info(uid,name,generation,creationTimestamp,minAvailable,maxUnAvailable,spec) VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return fmt.Errorf("cannot prepare pdb insert statement : %w", err)
	}
	d.insertPDB = pdbInsertStmt

	d.selectMaxPDBGeneration, err = db.Prepare("SELECT MAX(generation) from pdb_info where uid=?")
	if err != nil {
		return fmt.Errorf("cannot prepare pdb get max generation statement: %w", err)
	}

	d.updatePdbDeletionTimeStamp, err = db.Prepare("UPDATE pdb_info SET DeletionTimeStamp=? WHERE uid=?")
	if err != nil {
		return fmt.Errorf("cannot prepare updatePdbDeletionTimeStamp: %w", err)
	}

	d.updateNodeGroupDeletionTimeStamp, err = db.Prepare("UPDATE nodegroup_info SET DeletionTimeStamp=? WHERE Hash=?")
	if err != nil {
		return fmt.Errorf("cannot prepare update deletion timestamp for nodegroup: %w", err)
	}

	d.updateLatestNodeGroupSize, err = db.Prepare(UpdateLatestNodeGroupInfo)
	d.selectNodeGroupsBefore, err = db.Prepare(SelectNodeGroupBefore)
	if err != nil {
		return fmt.Errorf("cannot prepare selectNodeGroupsBefore statement: %w", err)
	}

	d.selectNodeGroupWithEventIDAndSameHash, err = db.Prepare(SelectNodeGroupBeforeEventUIDAndSameHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectNodeGroupWithEventIDAndSameHash: %w", err)
	}

	d.selectAllActiveNodeGroupsDesc, err = db.Prepare("SELECT * from nodegroup_info where  DeletionTimestamp is null  order by RowID desc")
	if err != nil {
		return fmt.Errorf("cannot prepare get active node groups  statement: %w", err)
	}
	d.selectNodeGroupHash, err = db.Prepare("SELECT Hash FROM nodegroup_info WHERE name=? ORDER BY RowID desc LIMIT 1")
	if err != nil {
		return fmt.Errorf("cannot prepare get node group hash statement: %w", err)
	}

	d.selectLatestNodeGroup, err = db.Prepare("SELECT * FROM nodegroup_info WHERE name=? ORDER BY RowID DESC LIMIT 1")
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestNodeGroup: %w", err)
	}

	d.selectLatestPodInfoWithName, err = db.Prepare("SELECT * FROM pod_info WHERE Name=? ORDER BY CreationTimestamp DESC LIMIT 1")
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

	//TODO: must create indexes
	d.selectPodCountWithUIDAndHash, err = db.Prepare(SelectPodCountWithUIDAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectPodCountWithUIDAndHash: %w", err)
	}

	d.updatePodDeletionTimeStamp, err = db.Prepare(UpdatePodDeletionTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare updatePodDeletionTimeStamp: %w", err)
	}

	d.insertNodeGroup, err = db.Prepare(InsertNodeGroupInfo)
	if err != nil {
		return fmt.Errorf("cannot prepare mcd insert statement : %w", err)
	}

	d.selectMaxNodeGroupGeneration, err = db.Prepare("select max(ShootGeneration) from nodegroup_info")
	if err != nil {
		return fmt.Errorf("cannot prepare selectMaxNodeGroupGeneration: %w", err)
	}
	d.selectMaxShootGen, err = db.Prepare("SELECT MAX(ShootGeneration) AS maxShootGen FROM nodegroup_info")
	if err != nil {
		return fmt.Errorf("cannot prepare selectMaxShootGen statement: %w", err)
	}

	d.selectEventWithUID, err = db.Prepare("SELECT * from event_info where UID = ?")

	d.selectAllEvents, err = db.Prepare("SELECT * from event_info ORDER BY EventTime")
	if err != nil {
		return fmt.Errorf("cannot prepare selectAllEvents statement: %w", err)
	}

	d.selectUnscheduledPodsBeforeTimestamp, err = db.Prepare(SelectPodsWithEmptyNameAndBeforeCreationTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectUnscheduledPodsBeforeTimestamp statement: %w", err)
	}

	d.selectScheduledPodsBeforeTimestamp, err = db.Prepare(SelectLatestScheduledPodsBeforeCreationTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectScheduledPodsBeforeTimestamp statement: %w", err)
	}

	d.selectLatestPodsBeforeTimestamp, err = db.Prepare(SelectLatestPodsBeforeCreationTimestamp)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestPodsBeforeTimestamp statement: %w", err)
	}

	d.selectNodeCountWithNameAndHash, err = db.Prepare(SelectNodeCountWithNameAndHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectNodeCountWithNameAndHash: %w", err)
	}

	d.selectCADeploymentByHash, err = db.Prepare(SelectCADeploymentByHash)
	if err != nil {
		return fmt.Errorf("cannot prepare selectCADeploymentByHash: %w", err)
	}

	d.selectLatestCADeployment, err = db.Prepare(SelectLatestCADeployment)
	if err != nil {
		return fmt.Errorf("cannot prepare selectLatestCADeployment")
	}

	d.selectEventCAAssocWithEventUID, err = db.Prepare(SelectEventCAAssocWithEventUID)
	if err != nil {
		return fmt.Errorf("cannot prepare selectEventCAAssocWithEventUID")
	}

	d.insertCADeployment, err = db.Prepare(InsertCADeployment)
	if err != nil {
		return fmt.Errorf("cannot prepare insertCADeployment statement")
	}

	d.insertEventCASettingsAssoc, err = db.Prepare(InsertEventCAAssoc)
	if err != nil {
		return fmt.Errorf("cannot prepare insertEventCASettingsAssoc")
	}

	d.selectLatestNodesBeforeAndNotDeleted, err = db.Prepare(SelectLatestNodesBeforeAndNotDeleted)
	if err != nil {
		return fmt.Errorf("cannot prepare ")
	}

	return err
}
func (d *DataAccess) createSchema() error {
	db := d.dataDB

	result, err := db.Exec(CreateEventInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create event_info table: %w", err)
	}

	slog.Info("successfully created event_info table", "result", result)

	result, err = db.Exec(CreateNodeGroupInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create nodegroup_info table: %w", err)
	}
	slog.Info("successfully created nodegroup_info table", "result", result)

	result, err = db.Exec(CreateNodeInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create node_info table : %w", err)
	}
	slog.Info("successfully created node_info table", "result", result)

	result, err = db.Exec(CreatePodInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create pod_info table: %w", err)
	}
	slog.Info("successfully created pod_info table", "result", result)

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

	result, err = db.Exec(CreateEventNodeGroupAssocTable)
	if err != nil {
		return fmt.Errorf("cannot create event_assoc table: %w", err)
	}
	slog.Info("successfully created event_nodegroup_assoc table", "result", result)

	result, err = db.Exec(CreateCASettingsInfoTable)
	if err != nil {
		return fmt.Errorf("cannot create ca_settings_info table: %w", err)
	}
	slog.Info("successfully created the ca_settings_info table")

	result, err = db.Exec(CreateEventCAAssocTable)
	if err != nil {
		return fmt.Errorf("cannot create event_ca_assoc table: %w", err)
	}
	slog.Info("successfully created the event_ca_assoc table")

	return nil
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

func (d *DataAccess) GetNodeGroupsWithEventUIDAndSameHash(eventUIDs []string) ([]NodeGroupWithEventUID, error) {
	rows, err := d.selectNodeGroupWithEventIDAndSameHash.Query()
	if err != nil {
		return nil, err
	}
	var nodeGroups []NodeGroupWithEventUID
	err = scan.Rows(&nodeGroups, rows)
	if err != nil {
		return nil, err
	}
	return lo.Filter(nodeGroups, func(item NodeGroupWithEventUID, index int) bool {
		isPresent := slices.ContainsFunc(eventUIDs, func(s string) bool {
			return s == item.EventUID
		})
		return isPresent
	}), nil
}

func (d *DataAccess) LoadNodeGroupsWithEventUIDAndSameHash() (nodeGroups []NodeGroupWithEventUID, err error) {
	rows, err := d.selectNodeGroupWithEventIDAndSameHash.Query()
	if err != nil {
		return
	}

	err = scan.Rows(&nodeGroups, rows)
	if err != nil {
		return
	}
	return
}

func (d *DataAccess) UpdatePodDeletionTimestamp(podUID types.UID, deletionTimestamp time.Time) (updated int64, err error) {
	result, err := d.updatePodDeletionTimeStamp.Exec(deletionTimestamp.UTC().UnixMilli(), podUID)
	if err != nil {
		return -1, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return -1, err
	}
	return updated, err
}

func (d *DataAccess) UpdateNodeInfoDeletionTimestamp(name string, deletionTimestamp time.Time) (updated int64, err error) {
	result, err := d.updateNodeInfoDeletionTimeStamp.Exec(deletionTimestamp, name)
	if err != nil {
		return -1, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return -1, err
	}
	return updated, err
}

func (d *DataAccess) UpdateLatestNodeGroupTargetSize(nodeGroupName string, targetSize int) (updated int64, err error) {
	result, err := d.updateLatestNodeGroupSize.Exec(targetSize, nodeGroupName)
	if err != nil {
		return -1, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return -1, err
	}
	return updated, err
}

func (d *DataAccess) UpdateNodeGroupDeletionTimestamp(nodeGroupHash string, deletionTimestamp time.Time) (updated int64, err error) {
	result, err := d.updateNodeGroupDeletionTimeStamp.Exec(deletionTimestamp, nodeGroupHash)
	if err != nil {
		return -1, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return -1, err
	}
	return updated, err
}

func (d *DataAccess) StoreEventInfo(event scalehist.EventInfo) error {
	//eventsStmt, err := db.Prepare("INSERT INTO event_info(UID, EventTime, ReportingController, Reason, Message, InvolvedObjectKind, InvolvedObjectName, InvolvedObjectNamespace, InvolvedObjectUID) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)")
	_, err := d.insertEvent.Exec(
		event.UID,
		event.EventTime,
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

func (d *DataAccess) GetNodeGroupHash(ngName string) (string, error) {
	row := d.selectNodeGroupHash.QueryRow(ngName)
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
func (d *DataAccess) LoadLatestNodeGroup(ngName string) (ng scalehist.NodeGroupInfo, err error) {
	rows, err := d.selectLatestNodeGroup.Query(ngName)
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Row(&ng, rows) //TODO: wrap err with msg and return
	return
}

func (d *DataAccess) LoadLatestPodInfoWithName(podName string) (podInfo scalehist.PodInfo, err error) {
	var podRow podRow
	rows, err := d.selectLatestPodInfoWithName.Query(podName)
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Row(&podRow, rows)
	if err != nil {
		return
	}
	podInfo, err = asPodInfo(podRow)
	return
}

func (d *DataAccess) StoreNodeGroup(ng scalehist.NodeGroupInfo) (rowID int64, err error) {
	if ng.Hash == "" {
		ng.Hash = ng.GetHash()
	}
	result, err := d.insertNodeGroup.Exec(ng.Name,
		ng.CreationTimestamp.UTC().UnixMilli(),
		ng.CurrentSize,
		ng.TargetSize,
		ng.MinSize,
		ng.MaxSize,
		ng.Zone,
		ng.MachineType,
		ng.Architecture,
		ng.ShootGeneration,
		ng.MCDGeneration,
		ng.PoolName,
		ng.PoolMin,
		ng.PoolMax,
		ng.Hash)
	if err != nil {
		slog.Error("cannot insert nodegroup in the nodegroup_info table", "error", err)
		return
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		slog.Error("cannot retrieve rowID for nodegroup from the nodegroup_info table", "error", err, "ng.name", ng.Name)
		return
	}
	slog.Info("storeNodeGroup successful.", "ng.Name", ng.Name,
		"ng.RowID", rowID,
		"ng.CurrentSize",
		ng.CurrentSize,
		"ng.MinSize",
		ng.MinSize,
		"ng.MaxSize",
		ng.MaxSize,
		"ng.Zone",
		ng.Zone,
		"ng.MCDGeneration",
		ng.MCDGeneration,
		"ng.PoolName",
		ng.PoolName,
		"ng.PoolMin",
		ng.PoolMin,
		"ng.PoolMax",
		ng.PoolMax,
		"ng.Hash",
		ng.Hash,
	)
	return
}

func (d *DataAccess) StoreEventNGAssoc(assoc scalehist.EventNodeGroupAssoc) error {
	_, err := d.insertEventNGAssoc.Exec(assoc.EventUID, assoc.NodeGroupRowID, assoc.NodeGroupHash)
	if err != nil {
		//TODO: give label
		return err
	}
	slog.Info("storeEventNGAssoc", "EventNodeGroupAssoc", assoc)
	return nil
}

func (d *DataAccess) StoreEventCASettingsAssoc(assoc scalehist.EventCASettingsAssoc) error {
	_, err := d.insertEventCASettingsAssoc.Exec(assoc.EventUID, assoc.CASettingsHash)
	if err != nil {
		//TODO: give label
		return err
	}
	slog.Info("storeEventCASettingsAssoc", "EventCASettingsAssoc", assoc)
	return nil
}

func (d *DataAccess) LoadEventNGAssocForEventUID(eventUID string) (assoc scalehist.EventNodeGroupAssoc, err error) {
	rows, err := d.selectEventNGAssocForEventUID.Query(eventUID)
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Row(&assoc, rows) //TODO: wrap err with msg and return
	return
}

func (d *DataAccess) GetMaxShootGeneration() (int, error) {
	rows, err := d.selectMaxShootGen.Query()
	if err != nil {
		return -1, nil
	}
	defer rows.Close()
	var maxShootGen int
	rows.Next()
	err = rows.Scan(&maxShootGen)
	if err != nil {
		return -1, nil
	}
	return maxShootGen, nil
}

func (d *DataAccess) GetMaxNodeGroupGeneration() (int, error) {
	var max sql.NullInt32
	err := d.selectMaxNodeGroupGeneration.QueryRow().Scan(&max)
	if max.Valid {
		return int(max.Int32), nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil
		}
	}
	return -1, err
}

func (d *DataAccess) GetMaxPDBGeneration(pdbUid string) (int, error) {
	row := d.selectMaxPDBGeneration.QueryRow(pdbUid)
	var generation sql.NullInt32
	err := row.Scan(&generation)
	if generation.Valid {
		return int(generation.Int32), nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil
		}
		return -1, err
	}
	return -1, nil
}

func (d *DataAccess) LoadEventInfoWithUID(eventUID string) (eventInfo scalehist.EventInfo, err error) {
	rows, err := d.selectEventWithUID.Query(eventUID)
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Row(&eventInfo, rows) //TODO: wrap err with msg and return
	return
}

func (d *DataAccess) LoadAllActiveNodeGroupsDesc() (nodeGroups []scalehist.NodeGroupInfo, err error) {
	rows, err := d.selectAllActiveNodeGroupsDesc.Query()
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	var nodeGroupRows []nodeGroupRow
	err = scan.Rows(&nodeGroupRows, rows) //TODO: wrap err with msg and return
	if err != nil {
		return
	}
	for _, row := range nodeGroupRows {
		nodeGroup, err := asNodeGroupInfo(row)
		if err != nil {
			return nil, err
		}
		nodeGroups = append(nodeGroups, nodeGroup)
	}
	return
}

func (d *DataAccess) LoadAllEvents() (events []scalehist.EventInfo, err error) {
	rows, err := d.selectAllEvents.Query()
	if err != nil { //TODO: wrap err with msg and return
		return
	}
	err = scan.Rows(&events, rows)
	return
}

func (d *DataAccess) GetLatestUnscheduledPodsBeforeTimestamp(timeStamp time.Time) ([]scalehist.PodInfo, error) {
	var podRows []podRow
	rows, err := d.selectUnscheduledPodsBeforeTimestamp.Query(timeStamp.UTC().UnixMilli(), timeStamp.UTC().UnixMilli())
	if err != nil { //TODO: wrap err with msg and return
		return nil, err
	}
	err = scan.Rows(&podRows, rows)
	var podInfos []scalehist.PodInfo
	for _, r := range podRows {
		podInfo, err := asPodInfo(r)
		if err != nil {
			return nil, err
		}
		podInfos = append(podInfos, podInfo)
	}
	return podInfos, nil
}

func (d *DataAccess) GetLatestPodsBeforeTimestamp(eventTime time.Time) (pods []scalehist.PodInfo, err error) {
	eventTimeMillis := eventTime.UTC().UnixMilli()
	rows, err := d.selectLatestPodsBeforeTimestamp.Query(eventTimeMillis, eventTimeMillis)
	var podRows []podRow
	if err != nil { //TODO: wrap err with msg and return
		return nil, err
	}
	err = scan.Rows(&podRows, rows)
	var podInfos []scalehist.PodInfo
	for _, r := range podRows {
		podInfo, err := asPodInfo(r)
		if err != nil {
			return nil, err
		}
		podInfos = append(podInfos, podInfo)
	}
	//slices.SortFunc(podInfos, func(a, b scalehist.PodInfo) int {
	//	return b.CreationTimestamp.Compare(a.CreationTimestamp)
	//})
	return podInfos, nil
}

func (d *DataAccess) GetLatestScheduledPodsBeforeTimestamp(eventTime time.Time) (pods []scalehist.PodInfo, err error) {
	eventTimeMillis := eventTime.UTC().UnixMilli()
	rows, err := d.selectScheduledPodsBeforeTimestamp.Query(eventTimeMillis, eventTimeMillis)
	var podRows []podRow
	if err != nil { //TODO: wrap err with msg and return
		return nil, err
	}
	err = scan.Rows(&podRows, rows)
	var podInfos []scalehist.PodInfo
	for _, r := range podRows {
		podInfo, err := asPodInfo(r)
		if err != nil {
			return nil, err
		}
		podInfos = append(podInfos, podInfo)
	}
	//slices.SortFunc(podInfos, func(a, b scalehist.PodInfo) int {
	//	return b.CreationTimestamp.Compare(a.CreationTimestamp)
	//})
	return podInfos, nil
}

func (d *DataAccess) GetLatestCADeployment() (caDeployment *scalehist.CASettingsInfo, err error) {
	rows, err := d.selectLatestCADeployment.Query()
	if err != nil {
		return
	}
	var caDeployments []scalehist.CASettingsInfo
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

func (d *DataAccess) GetCADeploymentWithHash(Hash string) (caDeployment *scalehist.CASettingsInfo, err error) {
	rows, err := d.selectLatestCADeployment.Query(Hash)
	if err != nil {
		return
	}
	var caDeployments []scalehist.CASettingsInfo
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

func (d *DataAccess) GetEventCAAssocWithEventUID(eventUID string) (eventCAAssoc *scalehist.EventCASettingsAssoc, err error) {
	rows, err := d.selectEventCAAssocWithEventUID.Query(eventUID)
	if err != nil {
		return
	}
	var eventCAAssocs []scalehist.EventCASettingsAssoc
	err = scan.Rows(&eventCAAssocs, rows)
	if err != nil {
		return nil, err
	}
	if len(eventCAAssocs) == 0 {
		return nil, nil
	}
	eventCAAssoc = &eventCAAssocs[0]
	return
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

func (d *DataAccess) StorePodInfo(podInfo scalehist.PodInfo) (int64, error) {
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
	//tolerations, err := tolerationsToText(podInfo.Spec.Tolerations)
	//if err != nil {
	//	return -1, err
	//}
	//tsc, err := tscToText(podInfo.Spec.TopologySpreadConstraints)
	podSpec, err := specToJson(podInfo.Spec)
	if err != nil {
		return -1, err
	}
	result, err := d.insertPodInfo.Exec(podInfo.UID, podInfo.Name, podInfo.Namespace, podInfo.CreationTimestamp.UTC().UnixMilli(),
		podInfo.NodeName, podInfo.NominatedNodeName, labels, requests, podSpec, podInfo.PodScheduleStatus, podInfo.Hash, 0)
	if err != nil {
		return -1, fmt.Errorf("could not persist podinfo %s: %w", podInfo, err)
	}
	slog.Info("stored row into pod_info.", "pod.Name", podInfo.Name, "pod.Namespace", podInfo.Namespace,
		"pod.CreationTimestamp", podInfo.CreationTimestamp, "pod.Hash", podInfo.Hash)
	return result.LastInsertId()
}

func (d *DataAccess) StoreNodeInfo(n scalehist.NodeInfo) (rowID int64, err error) {
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
	_, err = d.insertNodeInfo.Exec(n.Name, n.Namespace, n.CreationTimestamp.UTC().UnixMilli(), n.ProviderID, n.AllocatableVolumes, labelsText, taintsText,
		allocatableText, capacityText, n.Hash)
	if err != nil {
		slog.Error("cannot insert node_info in the node_info table", "error", err, "node", n)
		return
	}
	//rowID, err = result.LastInsertId()
	//if err != nil {
	//	slog.Error("cannot retrieve rowID for last inserted row from the nodegroup_info table", "error", err, "node.Name", n.Name, "node.CreationTimestamp", n.CreationTimestamp)
	//	return
	//}
	slog.Info("inserted new row into the node_info table", "node.Name", n.Name)
	return
}

func (d *DataAccess) LoadNodeInfosBefore(creationTimestamp time.Time) ([]scalehist.NodeInfo, error) {
	var nodeRowInfos []nodeRow
	rows, err := d.selectNodeInfosBefore.Query(creationTimestamp.UnixMilli())
	if err != nil { //TODO: wrap err with msg and return
		return nil, fmt.Errorf("cannot load from node_info from before creationTimestamp %q: %w", creationTimestamp, err)
	}
	cols, err := scan.Columns(&nodeRow{})
	slog.Info("columns for nodeRow", "columns", cols)
	err = scan.Rows(&nodeRowInfos, rows)
	if err != nil {
		return nil, fmt.Errorf("LoadNodeInfosBefore could not scal rows: %w", err)
	}
	var nodeInfos []scalehist.NodeInfo
	for _, r := range nodeRowInfos {
		nodeInfo, err := asNodeInfo(r)
		if err != nil {
			return nil, err
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}
	return nodeInfos, nil
}

func asNodeInfo(r nodeRow) (nodeInfo scalehist.NodeInfo, err error) {
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
	nodeInfo = scalehist.NodeInfo{
		Name:               r.Name,
		Namespace:          r.Namespace,
		CreationTimestamp:  time.UnixMilli(r.CreationTimestamp),
		ProviderID:         r.ProviderID,
		AllocatableVolumes: r.AllocatableVolumes,
		Labels:             labels,
		Taints:             taints,
		Allocatable:        allocatable,
		Capacity:           capacity,
		Hash:               r.Hash,
	}
	return
}

func asPodInfo(r podRow) (podInfo scalehist.PodInfo, err error) {
	labels, err := labelsFromText(r.Labels)
	if err != nil {
		return
	}
	requests, err := resourcesFromText(r.Requests)
	if err != nil {
		return
	}
	//tolerations, err := tolerationsFromText(r.Spec.Tolerations)
	//if err != nil {
	//	return
	//}
	spec, err := speccFromJson(r.Spec)
	//tsc, err := tscFromText(r.TopologySpreadConstraints)
	if err != nil {
		return
	}
	podInfo = scalehist.PodInfo{
		UID:               r.UID,
		Name:              r.Name,
		Namespace:         r.Namespace,
		CreationTimestamp: time.UnixMilli(r.CreationTimestamp),
		NodeName:          r.NodeName,
		NominatedNodeName: r.NominatedNodeName,
		Labels:            labels,
		Requests:          requests,
		Spec:              spec,
		PodScheduleStatus: scalehist.PodScheduleStatus(r.ScheduleStatus),
		Hash:              r.Hash,
	}
	podInfo.Hash = podInfo.GetHash()
	return
}

func asNodeGroupInfo(r nodeGroupRow) (nodeGroupInfo scalehist.NodeGroupInfo, err error) {
	nodeGroupInfo = scalehist.NodeGroupInfo{
		RowID:             r.RowID,
		Name:              r.Name,
		CreationTimestamp: time.UnixMilli(r.CreationTimestamp),
		CurrentSize:       r.CurrentSize,
		TargetSize:        r.TargetSize,
		MinSize:           r.MinSize,
		MaxSize:           r.MaxSize,
		Zone:              r.Zone,
		MachineType:       r.MachineType,
		Architecture:      r.Architecture,
		ShootGeneration:   r.ShootGeneration,
		MCDGeneration:     r.MCDGeneration,
		PoolName:          r.PoolName,
		PoolMin:           r.PoolMin,
		PoolMax:           r.PoolMax,
		Hash:              r.Hash,
	}
	nodeGroupInfo.Hash = nodeGroupInfo.GetHash()
	return
}

func (d *DataAccess) StoreCADeployment(caSettings scalehist.CASettingsInfo) (int64, error) {
	result, err := d.insertCADeployment.Exec(caSettings.Expander, caSettings.MaxNodesTotal, caSettings.Priorities, caSettings.Hash)
	if err != nil {
		return -1, err
	}
	return result.LastInsertId()
}

func (d *DataAccess) GetLatestNodesBeforeAndNotDeleted(timestamp time.Time) ([]scalehist.NodeInfo, error) {
	var nodeRowInfos []nodeRow
	rows, err := d.selectLatestNodesBeforeAndNotDeleted.Query(timestamp.UnixMilli())
	if err != nil { //TODO: wrap err with msg and return
		return nil, fmt.Errorf("cannot load from node_info from before creationTimestamp and not deleted %q: %w", timestamp, err)
	}
	cols, err := scan.Columns(&nodeRow{})
	slog.Info("columns for nodeRow", "columns", cols)
	err = scan.Rows(&nodeRowInfos, rows)
	if err != nil {
		return nil, fmt.Errorf("LoadNodeInfosBefore could not scal rows: %w", err)
	}
	var nodeInfos []scalehist.NodeInfo
	for _, r := range nodeRowInfos {
		nodeInfo, err := asNodeInfo(r)
		if err != nil {
			return nil, err
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}
	return nodeInfos, nil
}
