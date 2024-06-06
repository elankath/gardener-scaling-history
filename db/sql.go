package db

// TODO: move this to separate sql package. Move all the query literals into this sql package

const CreateNodeGroupInfoTable = `CREATE TABLE IF NOT EXISTS nodegroup_info(
	RowID INTEGER PRIMARY KEY AUTOINCREMENT,
	Name VARCHAR(256),
	CreationTimestamp INT NOT NULL,
	CurrentSize int,
	TargetSize int,
	MinSize int, 
	MaxSize int,
	Zone VARCHAR(128),
	MachineType TEXT, 
	Architecture VARCHAR(128),
	ShootGeneration INTEGER, 
	MCDGeneration INTEGER,
	PoolName VARCHAR(256), 
	PoolMin int,
	PoolMax int,
	Hash TEXT,
	DeletionTimestamp DATETIME)`

const InsertNodeGroupInfo = `INSERT INTO nodegroup_info(
	Name,
	CreationTimestamp,
	CurrentSize,
	TargetSize,
	MinSize, 
	MaxSize,
	Zone,
	MachineType, 
	Architecture,
	ShootGeneration, 
	MCDGeneration,
	PoolName, 
	PoolMin,
	PoolMax,
	Hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const UpdateLatestNodeGroupInfo = `UPDATE nodegroup_info SET TargetSize=? WHERE RowID=(SELECT max(RowID) FROM nodegroup_info where Name = ?)`
const SelectNodeGroupBefore = `SELECT * from nodegroup_info where CreationTimestamp < ? AND DeletionTimestamp is null ORDER BY CreationTimestamp DESC`
const SelectNodeGroupBeforeEventUIDAndSameHash = `SELECT 
	n.RowID,
	n.Name,
	n.CreationTimestamp, 
	n.CurrentSize,
	n.TargetSize, 
	n.MinSize, 
	n.MaxSize,
	n.Zone,
	n.MachineType,
	n.Architecture, 
	n.ShootGeneration,
	n.MCDGeneration,
	n.PoolName,
	n.PoolMin,
	n.PoolMax,
	n.Hash,
	e.EventUID 
	from nodegroup_info as n inner join event_nodegroup_assoc as e WHERE n.RowID = e.NodeGroupRowID`

const CreateNodeInfoTable = `CREATE TABLE IF NOT EXISTS node_info (
	Name TEXT, 
	Namespace TEXT, 
	CreationTimestamp DATETIME, 
	ProviderID TEXT, 
	AllocatableVolumes INTEGER,
	Labels TEXT, 
	Taints TEXT, 
	Allocatable TEXT, 
	Capacity TEXT, 
	Spec TEXT,
	Hash TEXT PRIMARY KEY,
	DeletionTimestamp DATETIME)`

const InsertNodeInfo = `INSERT INTO node_info(
	Name,
	Namespace,
	CreationTimestamp,
	ProviderID,
    AllocatableVolumes,
	Labels,
	Taints,
	Allocatable,
	Capacity,
	Hash) 
	VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const SelectNodeInfoBefore = `SELECT * FROM node_info WHERE CreationTimestamp < ? AND DeletionTimestamp IS NULL ORDER BY CreationTimestamp DESC`
const SelectNodeCountWithNameAndHash = "SELECT COUNT(*) from node_info where Name=? and Hash=?"
const UpdateNodeInfoDeletionTimestamp = `UPDATE node_info SET DeletionTimestamp = ? where Name = ?`

const CreatePodInfoTable = `CREATE TABLE IF NOT EXISTS pod_info (
	UID TEXT NOT NULL,
	Name TEXT NOT NULL,
	Namespace TEXT NOT NULL,
	CreationTimestamp INT,
	NodeName TEXT,
	NominatedNodeName TEXT,
	Labels TEXT,
	Requests TEXT,
	Spec TEXT,
	ScheduleStatus INTEGER,
	Hash TEXT PRIMARY KEY,
	DeletionTimestamp INT)`
const InsertPodInfo = `INSERT INTO pod_info(
	UID, 
	Name, 
	Namespace,
	CreationTimestamp,
	NodeName,
    NominatedNodeName,
	Labels,
	Requests,
    Spec,
    ScheduleStatus,
	Hash,
	DeletionTimestamp) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const UpdatePodDeletionTimestamp = "UPDATE pod_info SET DeletionTimestamp=? WHERE UID=?"
const SelectPodCountWithUIDAndHash = "SELECT COUNT(*) from pod_info where UID=? and Hash=?"
const SelectPodsWithEmptyNameAndBeforeCreationTimestamp = `SELECT * FROM (SELECT * from pod_info
               WHERE ScheduleStatus = 0 AND CreationTimestamp <= ? AND (DeletionTimestamp = 0 OR DeletionTimestamp >= ?)
               ORDER BY CreationTimestamp DESC) GROUP BY Name;`

const SelectLatestScheduledPodsBeforeCreationTimestamp = `SELECT * from (SELECT * FROM pod_info WHERE (ScheduleStatus = 1)  
                AND CreationTimestamp <= ? AND (DeletionTimestamp = 0 OR DeletionTimestamp >=  ?)  ORDER BY CreationTimestamp DESC) 
                GROUP BY Name;`
const SelectLatestNominatedPodsBeforeCreationTimestamp = `SELECT * from (SELECT * FROM pod_info WHERE (ScheduleStatus = 1)  
                AND CreationTimestamp <= ? AND (DeletionTimestamp = 0 OR DeletionTimestamp >=  ?)  ORDER BY CreationTimestamp DESC) 
                GROUP BY Name;`
const SelectLatestPodsBeforeCreationTimestamp = `SELECT * FROM pod_info WHERE
                CreationTimestamp <= ? AND (DeletionTimestamp = 0 OR DeletionTimestamp >=  ?)  ORDER BY CreationTimestamp DESC;`

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

const CreateEventNodeGroupAssocTable = `CREATE TABLE IF NOT EXISTS event_nodegroup_assoc(
	 EventUID TEXT,
	 NodeGroupRowID TEXT,
	 NodeGroupHash TEXT,
	 PRIMARY KEY (EventUID, NodeGroupRowID))`

const InsertEventNodeGroupAssoc = `INSERT INTO event_nodegroup_assoc(EventUID, NodeGroupRowID, NodeGroupHash) 
	VALUES(?,?,?) ON CONFLICT(EventUID, NodeGroupRowID) DO NOTHING`

const CreateCASettingsInfoTable = `CREATE TABLE IF NOT EXISTS ca_settings_info(
    Id INTEGER PRIMARY KEY AUTOINCREMENT,
    Expander TEXT,
    MaxNodesTotal INT,
	Priorities TEXT,
	Hash TEXT)`

const SelectCADeploymentByHash = `SELECT * FROM ca_settings_info WHERE Hash=?`

const SelectLatestCADeployment = `SELECT * FROM ca_settings_info ORDER BY Id DESC LIMIT 1`

const InsertCADeployment = `INSERT INTO ca_settings_info (
    Expander,
    MaxNodesTotal,
	Priorities,
    Hash
) VALUES (? ,? , ? ,?)`

const CreateEventCAAssocTable = `CREATE TABLE IF NOT EXISTS event_ca_assoc(
    EventUID TEXT PRIMARY KEY ,
    CASettingsHash TEXT
)`

const InsertEventCAAssoc = `INSERT INTO event_ca_assoc(
    EventUID,
    CASettingsHash
) VALUES(?,?)`

const SelectEventCAAssocWithEventUID = `SELECT * FROM event_ca_assoc WHERE EventUID = ?`

const SelectLatestNodesBeforeAndNotDeleted = `SELECT * from (select * from node_info where node_info.CreationTimestamp <= ? and node_info.DeletionTimestamp is null order by RowID DESC) GROUP by Name`
