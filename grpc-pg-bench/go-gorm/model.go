package main

// GORM models mapped to the shared benchmark tables. Column names are given
// explicitly (GORM's default naming would snake_case the field names, which
// already matches here, but the explicit tags keep it unambiguous). No
// gorm.Model embed and no CreatedAt/UpdatedAt fields, so GORM does NOT manage
// timestamps — `received_at` / `created_at` use their DB `DEFAULT now()`, like
// every other stack.

type Command struct {
	ID          int64  `gorm:"column:id;primaryKey"`
	WorkflowID  string `gorm:"column:workflow_id"`
	CommandType string `gorm:"column:command_type"`
	Payload     string `gorm:"column:payload"`
	Seq         int64  `gorm:"column:seq"`
	Checksum    int64  `gorm:"column:checksum"`
}

func (Command) TableName() string { return "commands" }

type OutboxEvent struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	WorkflowID string `gorm:"column:workflow_id"`
	EventType  string `gorm:"column:event_type"`
	Payload    string `gorm:"column:payload"`
}

func (OutboxEvent) TableName() string { return "outbox" }

// StateRow is the projected result of GetState (timestamp pre-converted to
// micros server-side, like the other stacks). Scanned from a raw query, so it
// is not a GORM-managed table model.
type StateRow struct {
	WorkflowID      string
	State           string
	Version         int64
	UpdatedAtMicros int64
}
