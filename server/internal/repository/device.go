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

// 公共字段列表，避免每个查询重写一遍。
const deviceColumns = `device_id, owner_id, type, name, room, status, token, bind_code, last_seen, created_at, updated_at`

// scanDevice 把一行结果扫到 *model.Device，处理 owner_id NULL。
func scanDevice(row interface {
	Scan(dest ...any) error
}) (*model.Device, error) {
	d := &model.Device{}
	var owner sql.NullInt64
	if err := row.Scan(
		&d.DeviceID, &owner, &d.Type, &d.Name, &d.Room, &d.Status,
		&d.Token, &d.BindCode, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if owner.Valid {
		v := owner.Int64
		d.OwnerID = &v
	}
	return d, nil
}

// Create 插入一条设备记录。
func (r *DeviceRepo) Create(ctx context.Context, d *model.Device) error {
	var owner any
	if d.OwnerID != nil {
		owner = *d.OwnerID
	} else {
		owner = nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO devices (device_id, owner_id, type, name, room, status, token, bind_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.DeviceID, owner, d.Type, d.Name, d.Room, d.Status, d.Token, d.BindCode,
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

// GetByID 按 device_id 查询设备。
func (r *DeviceRepo) GetByID(ctx context.Context, deviceID string) (*model.Device, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE device_id = ?`, deviceID,
	)
	d, err := scanDevice(row)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

// ListByOwner 查询某用户的所有设备。
func (r *DeviceRepo) ListByOwner(ctx context.Context, ownerID int64) ([]*model.Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deviceColumns+`
		 FROM devices WHERE owner_id = ? ORDER BY created_at`, ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list devices by owner: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ListBindable 查询所有处于 waiting_bind 状态、尚未归属任何用户的池中设备。
// 用户在 Android 端"添加设备"页看到这个列表，并通过 bind_code 完成绑定。
func (r *DeviceRepo) ListBindable(ctx context.Context) ([]*model.Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deviceColumns+`
		 FROM devices
		 WHERE owner_id IS NULL AND status = 'waiting_bind'
		 ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list bindable devices: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
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

// BindToOwner 把待绑定池中的设备绑定到指定用户：
//
//	owner_id = userID
//	name / room 用用户输入覆盖（保留原 type / token / device_id）
//	status     置为 online（设备已可控制）
//	bind_code  清空（防止再次绑定）
//
// 仅当 device_id + bind_code 严格匹配且 owner_id 仍为 NULL 时才生效；
// 受影响行数 0 表示"绑定码错误 / 设备已被绑定 / 设备不存在"——返回 sql.ErrNoRows。
func (r *DeviceRepo) BindToOwner(ctx context.Context, deviceID, bindCode string, userID int64, name, room string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE devices
		 SET owner_id = ?, name = ?, room = ?, status = 'online', bind_code = ''
		 WHERE device_id = ? AND bind_code = ? AND owner_id IS NULL`,
		userID, name, room, deviceID, bindCode,
	)
	if err != nil {
		return fmt.Errorf("bind device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bind device rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Rename 修改设备的展示名 / 房间。仅在归属匹配时更新。
// 受影响行数 0 表示"设备不存在 / 非归属者"。
func (r *DeviceRepo) Rename(ctx context.Context, deviceID string, ownerID int64, name, room string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE devices SET name = ?, room = ? WHERE device_id = ? AND owner_id = ?`,
		name, room, deviceID, ownerID,
	)
	if err != nil {
		return fmt.Errorf("rename device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rename rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
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
