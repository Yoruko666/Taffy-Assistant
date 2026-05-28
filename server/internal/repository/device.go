package repository

import (
	"context"
	"database/sql"
	"fmt"

	"taffy-server/internal/model"
)

// DeviceRepo 设备表数据访问。
type DeviceRepo struct {
	db *sql.DB
}

// NewDeviceRepo 创建 DeviceRepo。
func NewDeviceRepo(db *sql.DB) *DeviceRepo {
	return &DeviceRepo{db: db}
}

// Create 插入一条设备记录。
func (r *DeviceRepo) Create(ctx context.Context, d *model.Device) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO devices (device_id, owner_id, type, name, room, status, token)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.DeviceID, d.OwnerID, d.Type, d.Name, d.Room, d.Status, d.Token,
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

// GetByID 按 device_id 查询设备。
func (r *DeviceRepo) GetByID(ctx context.Context, deviceID string) (*model.Device, error) {
	d := &model.Device{}
	err := r.db.QueryRowContext(ctx,
		`SELECT device_id, owner_id, type, name, room, status, token, last_seen, created_at, updated_at
		 FROM devices WHERE device_id = ?`, deviceID,
	).Scan(&d.DeviceID, &d.OwnerID, &d.Type, &d.Name, &d.Room, &d.Status, &d.Token, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

// ListByOwner 查询某用户的所有设备。
func (r *DeviceRepo) ListByOwner(ctx context.Context, ownerID int64) ([]*model.Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT device_id, owner_id, type, name, room, status, token, last_seen, created_at, updated_at
		 FROM devices WHERE owner_id = ? ORDER BY created_at`, ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list devices by owner: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		d := &model.Device{}
		if err := rows.Scan(&d.DeviceID, &d.OwnerID, &d.Type, &d.Name, &d.Room, &d.Status, &d.Token, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// UpdateStatus 更新设备状态。
func (r *DeviceRepo) UpdateStatus(ctx context.Context, deviceID string, status model.DeviceStatus) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE devices SET status = ? WHERE device_id = ?`, status, deviceID,
	)
	if err != nil {
		return fmt.Errorf("update device status: %w", err)
	}
	return nil
}

// UpdateLastSeen 更新设备最后在线时间。
func (r *DeviceRepo) UpdateLastSeen(ctx context.Context, deviceID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE devices SET last_seen = NOW(), status = 'online' WHERE device_id = ?`, deviceID,
	)
	if err != nil {
		return fmt.Errorf("update device last_seen: %w", err)
	}
	return nil
}

// Delete 删除设备。
func (r *DeviceRepo) Delete(ctx context.Context, deviceID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM devices WHERE device_id = ?`, deviceID,
	)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}
