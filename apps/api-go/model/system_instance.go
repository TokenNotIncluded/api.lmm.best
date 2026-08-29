package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SystemInstanceStatusOnline = "online"
	SystemInstanceStatusStale  = "stale"

	SystemInstanceStaleAfterSeconds int64 = 90
)

type SystemInstance struct {
	// NodeName is the legacy database key and API field. For a slotted Go
	// reporter it contains the derived reporter ID (for example host@blue).
	// Unslotted reporters continue to store the physical node name unchanged.
	NodeName   string `json:"node_name" gorm:"type:varchar(128);primaryKey"`
	Info       string `json:"info" gorm:"type:text"`
	StartedAt  int64  `json:"started_at" gorm:"bigint;index"`
	LastSeenAt int64  `json:"last_seen_at" gorm:"bigint;index"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt  int64  `json:"updated_at" gorm:"bigint;index"`
}

type SystemInstanceResponse struct {
	// NodeName remains the legacy reporter key for API compatibility.
	NodeName          string `json:"node_name"`
	ReporterID        string `json:"reporter_id"`
	PhysicalNodeName  string `json:"physical_node_name"`
	InstanceSlot      string `json:"instance_slot,omitempty"`
	Status            string `json:"status"`
	StaleAfterSeconds int64  `json:"stale_after_seconds"`
	StartedAt         int64  `json:"started_at"`
	LastSeenAt        int64  `json:"last_seen_at"`
	Info              any    `json:"info"`
}

func (instance *SystemInstance) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if instance.CreatedAt == 0 {
		instance.CreatedAt = now
	}
	if instance.UpdatedAt == 0 {
		instance.UpdatedAt = now
	}
	return nil
}

func UpsertSystemInstance(nodeName string, info any, startedAt int64, lastSeenAt int64) error {
	infoText, err := marshalSystemInstanceInfo(info)
	if err != nil {
		return err
	}
	if lastSeenAt == 0 {
		lastSeenAt = common.GetTimestamp()
	}
	instance := &SystemInstance{
		NodeName:   nodeName,
		Info:       infoText,
		StartedAt:  startedAt,
		LastSeenAt: lastSeenAt,
		UpdatedAt:  lastSeenAt,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"info",
			"started_at",
			"last_seen_at",
			"updated_at",
		}),
	}).Create(instance).Error
}

func ListSystemInstances() ([]*SystemInstance, error) {
	var instances []*SystemInstance
	err := DB.Order("last_seen_at desc").Find(&instances).Error
	return instances, err
}

func DeleteStaleSystemInstances(now int64) (int64, error) {
	result := DB.Where("last_seen_at < ?", now-SystemInstanceStaleAfterSeconds).Delete(&SystemInstance{})
	return result.RowsAffected, result.Error
}

// DeleteStaleSystemInstance deletes exactly one reporter identity. Slotted
// runtimes on the same physical node therefore age out independently.
func DeleteStaleSystemInstance(reporterID string, now int64) (bool, error) {
	result := DB.Where("node_name = ? AND last_seen_at < ?", reporterID, now-SystemInstanceStaleAfterSeconds).Delete(&SystemInstance{})
	return result.RowsAffected > 0, result.Error
}

func (instance *SystemInstance) ToResponse(now int64) SystemInstanceResponse {
	status := SystemInstanceStatusOnline
	if now-instance.LastSeenAt > SystemInstanceStaleAfterSeconds {
		status = SystemInstanceStatusStale
	}
	info := decodeSystemInstanceInfo(instance.Info)
	physicalNodeName := nestedSystemInstanceInfoString(info, "node", "name")
	if physicalNodeName == "" {
		physicalNodeName = instance.NodeName
	}
	instanceSlot := nestedSystemInstanceInfoString(info, "reporter", "slot")
	if instanceSlot == "" {
		instanceSlot = nestedSystemInstanceInfoString(info, "runtime", "instance_slot")
	}
	return SystemInstanceResponse{
		NodeName:          instance.NodeName,
		ReporterID:        instance.NodeName,
		PhysicalNodeName:  physicalNodeName,
		InstanceSlot:      instanceSlot,
		Status:            status,
		StaleAfterSeconds: SystemInstanceStaleAfterSeconds,
		StartedAt:         instance.StartedAt,
		LastSeenAt:        instance.LastSeenAt,
		Info:              info,
	}
}

func nestedSystemInstanceInfoString(info any, section string, field string) string {
	root, ok := info.(map[string]any)
	if !ok {
		return ""
	}
	nested, ok := root[section].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := nested[field].(string)
	return value
}

func marshalSystemInstanceInfo(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := common.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeSystemInstanceInfo(data string) any {
	if data == "" {
		return nil
	}
	var value any
	if err := common.UnmarshalJsonStr(data, &value); err != nil {
		return data
	}
	return value
}
