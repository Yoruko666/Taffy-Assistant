-- Taffy 数据库迁移 003：设备绑定（UC-03）支持
-- 执行: mysql -u root -p < 003_device_binding.sql
--
-- 变更点：
--   1. devices.owner_id 改为可空 —— 待绑定池中的设备没有归属用户；
--   2. devices 新增 bind_code 字段 —— 用户绑定时输入的 6 位短码；
--   3. 插入若干 status='waiting_bind' 的待绑定演示设备，供 Android 端 UC-03 演示。

USE taffy;

-- ============================================================
-- 1. owner_id 改为 NULL 允许（待绑定设备不挂任何用户）
-- ============================================================
-- 先删除原外键约束（MySQL 8.0 自动生成的名字一般是 devices_ibfk_1）
SET @fk_name = (
    SELECT CONSTRAINT_NAME
    FROM information_schema.KEY_COLUMN_USAGE
    WHERE TABLE_SCHEMA = 'taffy'
      AND TABLE_NAME = 'devices'
      AND REFERENCED_TABLE_NAME = 'users'
    LIMIT 1
);
SET @stmt = CONCAT('ALTER TABLE devices DROP FOREIGN KEY ', @fk_name);
PREPARE s FROM @stmt; EXECUTE s; DEALLOCATE PREPARE s;

ALTER TABLE devices
    MODIFY COLUMN owner_id BIGINT NULL COMMENT '归属用户；NULL 表示尚未绑定';

ALTER TABLE devices
    ADD CONSTRAINT fk_devices_owner
        FOREIGN KEY (owner_id) REFERENCES users(user_id) ON DELETE SET NULL;

-- ============================================================
-- 2. 新增 bind_code 字段
-- ============================================================
ALTER TABLE devices
    ADD COLUMN bind_code VARCHAR(16) NOT NULL DEFAULT ''
        COMMENT '6 位绑定码；空字符串表示已绑定或不可绑定'
        AFTER token;

-- ============================================================
-- 3. 待绑定设备种子（演示用）
-- ============================================================
-- 这些设备处于 waiting_bind 状态，owner_id=NULL，
-- 用户在 Android 端"添加设备"页输入下方 bind_code 即可绑定到自己账户。
INSERT INTO devices (device_id, owner_id, type, name, room, status, token, bind_code) VALUES
('light-pool-001',  NULL, 'LIGHT',   '新灯泡',    '', 'waiting_bind', 'tk_pool_light_001',  '888001'),
('light-pool-002',  NULL, 'LIGHT',   '新灯泡',    '', 'waiting_bind', 'tk_pool_light_002',  '888002'),
('aircon-pool-001', NULL, 'AIRCON',  '新空调',    '', 'waiting_bind', 'tk_pool_aircon_001', '888003'),
('curtain-pool-001',NULL, 'CURTAIN', '新窗帘',    '', 'waiting_bind', 'tk_pool_curtain_001','888004'),
('socket-pool-001', NULL, 'SOCKET',  '新插座',    '', 'waiting_bind', 'tk_pool_socket_001', '888005'),
('speaker-pool-001',NULL, 'SPEAKER', '新音箱小菲','', 'waiting_bind', 'tk_pool_speaker_001','888006');

-- 待绑定设备也要有初始 device_states（绑定后无需再插入）
INSERT INTO device_states (device_id, power) VALUES
('light-pool-001',   FALSE),
('light-pool-002',   FALSE),
('aircon-pool-001',  FALSE),
('curtain-pool-001', FALSE),
('socket-pool-001',  FALSE),
('speaker-pool-001', FALSE);
