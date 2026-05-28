package repository

import (
	"context"
	"database/sql"
	"fmt"

	"taffy-server/internal/model"
)

// DeviceStateRepo 设备状态表数据访问。
type DeviceStateRepo struct {
	db *sql.DB
}

// NewDeviceStateRepo 创建 DeviceStateRepo。
func NewDeviceStateRepo(db *sql.DB) *DeviceStateRepo {
	return &DeviceStateRepo{db: db}
}

// Get 查询某设备的当前状态。
func (r *DeviceStateRepo) Get(ctx context.Context, deviceID string) (*model.DeviceState, error) {
	s := &model.DeviceState{}
	err := r.db.QueryRowContext(ctx,
		`SELECT device_id, power, brightness, temperature, mode, position, updated_at
		 FROM device_states WHERE device_id = ?`, deviceID,
	).Scan(&s.DeviceID, &s.Power, &s.Brightness, &s.Temperature, &s.Mode, &s.Position, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get device state: %w", err)
	}
	return s, nil
}

// Upsert 插入或更新设备状态（按 device_id）。
func (r *DeviceStateRepo) Upsert(ctx context.Context, s *model.DeviceState) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO device_states (device_id, power, brightness, temperature, mode, position)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   power = VALUES(power),
		   brightness = VALUES(brightness),
		   temperature = VALUES(temperature),
		   mode = VALUES(mode),
		   position = VALUES(position)`,
		s.DeviceID, s.Power, s.Brightness, s.Temperature, s.Mode, s.Position,
	)
	if err != nil {
		return fmt.Errorf("upsert device state: %w", err)
	}
	return nil
}

// ListByOwner 查询某用户所有设备的状态（联表查询）。
func (r *DeviceStateRepo) ListByOwner(ctx context.Context, ownerID int64) ([]*model.DeviceState, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.device_id, s.power, s.brightness, s.temperature, s.mode, s.position, s.updated_at
		 FROM device_states s
		 JOIN devices d ON s.device_id = d.device_id
		 WHERE d.owner_id = ?
		 ORDER BY d.room, d.name`, ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list device states by owner: %w", err)
	}
	defer rows.Close()

	var states []*model.DeviceState
	for rows.Next() {
		s := &model.DeviceState{}
		if err := rows.Scan(&s.DeviceID, &s.Power, &s.Brightness, &s.Temperature, &s.Mode, &s.Position, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan device state: %w", err)
		}
		states = append(states, s)
	}
	return states, rows.Err()
}
