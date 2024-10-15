package db

const CreateRecorderStateInfo = `CREATE TABLE IF NOT EXISTS recorder_state_info(
    BeginTimestamp INT NOT NULL )`

const InsertRecorderStateInfo = `INSERT INTO recorder_state_info(
    BeginTimestamp
) VALUES (?)`

const SelectInitialRecorderStateInfo = `SELECT * FROM recorder_state_info ORDER BY BeginTimestamp  LIMIT 1`

const CreateWorkerPoolInfo = `CREATE TABLE IF NOT EXISTS worker_pool_info(
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT,
	Namespace TEXT,
	MachineType      TEXT, 
	Architecture      TEXT,
	Minimum           INT,
	Maximum           INT,
	MaxSurge          TEXT,
	MaxUnavailable    TEXT,
	Zones             TEXT,
	DeletionTimestamp INT,
	Hash TEXT)`

const InsertWorkerPoolInfo = `INSERT INTO worker_pool_info(
	CreationTimestamp,
	SnapshotTimestamp,
	Name,
	Namespace,
	MachineType,
	Architecture,
	Minimum,                       
	Maximum,
	MaxSurge,
	MaxUnavailable ,
	Zones,
	Hash
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const SelectWorkerPoolInfoBefore = `SELECT * FROM worker_pool_info where SnapshotTimestamp <= ?  GROUP BY Name HAVING max(RowID)`
const SelectAllWorkerPoolInfoHashes = "SELECT RowID, Name, SnapshotTimestamp, Hash FROM worker_pool_info ORDER BY RowID desc"

const CreateMCDInfoTable = `CREATE TABLE IF NOT EXISTS mcd_info(
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT,
	Namespace TEXT,
	Replicas INTEGER,
	PoolName TEXT,
	Zone TEXT,
	MaxSurge TEXT,
	MaxUnavailable TEXT, 
	MachineClassName TEXT,
	Labels TEXT,
	Taints TEXT,
	DeletionTimestamp INT,
	Hash TEXT)`
const InsertMCDInfo = `INSERT INTO mcd_info(
	CreationTimestamp,
	SnapshotTimestamp,
	Name,
	Namespace,
	Replicas,
	PoolName,
	Zone,
	MaxSurge,
	MaxUnavailable,
	MachineClassName,
	Labels,
	Taints,
	Hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const SelectLatestMCDInfoBefore = `SELECT * FROM mcd_info where SnapshotTimestamp <= ?  GROUP BY Name HAVING max(RowID) ORDER BY RowID ASC`
const UpdateMCDInfoDeletionTimestamp = `UPDATE mcd_info SET DeletionTimestamp = ? where Name = ?`
const SelectMCDInfoHash = "SELECT Hash FROM mcd_info WHERE name=? ORDER BY RowID desc LIMIT 1"
const SelectLatestMCDInfo = "SELECT * FROM mcd_info WHERE name=? ORDER BY RowID DESC LIMIT 1"

const CreateMCCInfoTable = `CREATE TABLE IF NOT EXISTS mcc_info(
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT,
	Namespace TEXT,
	InstanceType TEXT,
	PoolName TEXT,
	Region TEXT,
	Zone TEXT,
	Labels TEXT,
	Capacity TEXT,
	DeletionTimestamp INT,
	Hash TEXT)`
const InsertMCCInfo = `INSERT INTO mcc_info(
	CreationTimestamp,
	SnapshotTimestamp,
	Name,
	Namespace,
	InstanceType,
	PoolName,
	Region,
	Zone,
	Labels,
	Capacity,
	Hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const SelectLatestMCCInfoBefore = `SELECT * FROM mcc_info where SnapshotTimestamp <= ?  GROUP BY Name HAVING max(RowID) ORDER BY RowID ASC`
const UpdateMCCInfoDeletionTimestamp = `UPDATE mcc_info SET DeletionTimestamp = ? where Name = ?`
const SelectMCCInfoHash = "SELECT Hash FROM mcc_info WHERE name=? ORDER BY RowID desc LIMIT 1"
const SelectLatestMCCInfo = "SELECT * FROM mcc_info WHERE name=? ORDER BY RowID DESC LIMIT 1"

const CreateNodeInfoTable = `CREATE TABLE IF NOT EXISTS node_info (
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT, 
	Namespace TEXT, 
	ProviderID TEXT, 
	AllocatableVolumes INT,
	Labels TEXT, 
	Taints TEXT, 
	Allocatable TEXT, 
	Capacity TEXT, 
	DeletionTimestamp INT,
	Hash TEXT)`

const CreateCSINodeTable = `CREATE TABLE IF NOT EXISTS csi_node_info (
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT, 
	Namespace TEXT, 
	AllocatableVolumes INT,
	DeletionTimestamp INT
)`

const InsertCSINodeInfo = `INSERT INTO csi_node_info(
    CreationTimestamp,
	SnapshotTimestamp,
	Name,
	Namespace,
    AllocatableVolumes) 
	VALUES(?, ?, ?, ?, ?)`

const SelectCSINodeInfoBefore = `SELECT * FROM csi_node_info
	WHERE csi_node_info.CreationTimestamp <= ?
	AND (DeletionTimestamp is null OR DeletionTimestamp >=  ?)
	GROUP BY csi_node_info.Name HAVING max(RowID) ORDER BY RowID ASC`

const UpdateCSINodeInfoDeletionTimestamp = `UPDATE csi_node_info SET DeletionTimestamp = ? where Name = ?`

const InsertNodeInfo = `INSERT INTO node_info(
    CreationTimestamp,
	SnapshotTimestamp,
	Name,
	Namespace,
	ProviderID,
    AllocatableVolumes,
	Labels,
	Taints,
	Allocatable,
	Capacity,
	Hash) 
	VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const SelectNodeInfoBefore = `SELECT * FROM node_info 
	WHERE node_info.CreationTimestamp <= ?
	AND (DeletionTimestamp is null OR DeletionTimestamp >=  ?)
	GROUP BY node_info.Name HAVING max(RowID) ORDER BY RowID ASC`

const SelectNodeCountWithNameAndHash = "SELECT COUNT(*) from node_info where Name=? and Hash=?"
const UpdateNodeInfoDeletionTimestamp = `UPDATE node_info SET DeletionTimestamp = ? where Name = ?`

const CreatePodInfoTable = `CREATE TABLE IF NOT EXISTS pod_info (
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT,
	Namespace TEXT,
	UID TEXT NOT NULL,
	NodeName TEXT,
	NominatedNodeName TEXT,
	Phase TEXT,
	Labels TEXT,
	Requests TEXT,
	Spec TEXT,
	ScheduleStatus INTEGER,
	DeletionTimestamp INT,
	Hash TEXT)`
const InsertPodInfo = `INSERT INTO pod_info(
    CreationTimestamp,
	SnapshotTimestamp,
	Name, 
	Namespace,
	UID, 
	NodeName,
    NominatedNodeName,
    Phase,
	Labels,
	Requests,
    Spec,
    ScheduleStatus,
	Hash) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const UpdatePodDeletionTimestamp = "UPDATE pod_info SET DeletionTimestamp=? WHERE UID=?"
const SelectPodCountWithUIDAndHash = "SELECT COUNT(*) from pod_info where UID=? and Hash=?"
const SelectUnscheduledPodsBeforeSnapshotTimestamp = `SELECT * FROM (SELECT * from pod_info
               WHERE ScheduleStatus = 0 AND SnapshotTimestamp <= ? AND (DeletionTimestamp is null OR DeletionTimestamp >= ?)
               ORDER BY SnapshotTimestamp DESC) GROUP BY Name;`

const SelectLatestScheduledPodsBeforeSnapshotTimestamp = `SELECT * from (SELECT * FROM pod_info WHERE (ScheduleStatus = 1)  
                AND SnapshotTimestamp <= ? AND (DeletionTimestamp is null OR DeletionTimestamp >=  ?)  ORDER BY SnapshotTimestamp DESC) 
                GROUP BY Name;`
const SelectLatestPodsBeforeSnapshotTimestamp = `SELECT * FROM pod_info 
	WHERE SnapshotTimestamp  <= ?
	AND (DeletionTimestamp is null OR DeletionTimestamp >=  ?)  
	GROUP BY pod_info.UID HAVING max(RowID) ORDER BY RowID ASC;`

/*
At time <=X1, get all the pod infos who
between time >x1 and time <=X2, get all the pod infos who are in Pending Running Phase.
*/

const CreatePriorityClassInfoTable = `CREATE TABLE IF NOT EXISTS pc_info (
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	CreationTimestamp INT NOT NULL,
	SnapshotTimestamp INT NOT NULL,
	Name TEXT,
	UID TEXT,
	Value INT NOT NULL,
	GlobalDefault BOOLEAN,
	PreemptionPolicy TEXT,
	Description TEXT,
	Labels TEXT,
	DeletionTimestamp INT,
	Hash TEXT)`

const InsertPriorityClassInfo = `INSERT INTO pc_info(
    CreationTimestamp,
	SnapshotTimestamp,
	Name, 
	UID, 
	Value,
    GlobalDefault,
	PreemptionPolicy,
	Description,
    Labels,
	Hash) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const SelectLatestPriorityClassInfoBeforeSnapshotTimestamp = `SELECT * FROM pc_info WHERE
                CreationTimestamp <= ? AND (DeletionTimestamp is null OR DeletionTimestamp >=  ?)   GROUP BY pc_info.Name HAVING max(RowID);`
const UpdatePriorityClassInfoDeletionTimestamp = "UPDATE pc_info SET DeletionTimestamp=? WHERE UID=?"
const SelectPriorityClassInfoCountWithUIDAndHash = "SELECT COUNT(*) from pc_info where UID=? and Hash=?"

const CreateEventInfoTable = `CREATE TABLE IF NOT EXISTS event_info(
	UID varchar(128) PRIMARY KEY,
	EventTime DATETIME NOT NULL,
	ReportingController VARCHAR(256),
	Reason VARCHAR(128),
	Message TEXT,
	InvolvedObjectKind varchar(128),
	InvolvedObjectName varchar(128),
	InvolvedObjectNamespace varchar(128),
	InvolvedObjectUID varchar(128))`

const InsertEvent = `INSERT INTO event_info(
	UID,
	EventTime,
	ReportingController,
	Reason,
	Message,
	InvolvedObjectKind,
	InvolvedObjectName,
	InvolvedObjectNamespace,
	InvolvedObjectUID) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(UID) DO NOTHING`

const CreateCASettingsInfoTable = `CREATE TABLE IF NOT EXISTS ca_settings_info(
    RowID INTEGER PRIMARY KEY AUTOINCREMENT,
    SnapshotTimestamp INT NOT NULL,
    Expander TEXT,
    ScanInterval INT,
    MaxNodeProvisionTime          INT,
    MaxGracefulTerminationSeconds INT,
    NewPodScaleUpDelay            INT,
    MaxEmptyBulkDelete            INT,
    IgnoreDaemonSetUtilization    BOOLEAN,
    MaxNodesTotal                 INT,
	NodeGroupsMinMax TEXT,
	Priorities TEXT,
	Hash TEXT)`

const SelectCADeploymentByHash = `SELECT * FROM ca_settings_info WHERE Hash=?`

const SelectLatestCASettingsInfo = `SELECT * FROM ca_settings_info ORDER BY RowID DESC LIMIT 1`

const InsertCASettingsInfo = `INSERT INTO ca_settings_info (
    SnapshotTimestamp,                   
    Expander,
    ScanInterval,
    MaxNodeProvisionTime,
    MaxGracefulTerminationSeconds,
    NewPodScaleUpDelay,
    MaxEmptyBulkDelete, 
    IgnoreDaemonSetUtilization,
    MaxNodesTotal,
    NodeGroupsMinMax,
	Priorities,
    Hash
) VALUES (? ,? , ? ,?, ?, ?, ? , ? , ? , ? , ?, ?)`

const SelectLatestCASettingsBefore = `SELECT * from ca_settings_info WHERE SnapshotTimestamp <= ? ORDER BY RowID DESC LIMIT 1`
const SelectLatestNodesBeforeAndNotDeleted = `SELECT * from (select * from node_info where node_info.CreationTimestamp <= ? and node_info.DeletionTimestamp = 0 order by RowID DESC) GROUP by Name`
const SelectTriggerScaleUpEvents = `SELECT * FROM event_info where Reason = 'TriggeredScaleUp' order by EventTime asc`
