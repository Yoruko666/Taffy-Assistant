-- Taffy 测试种子数据
-- 执行: mysql -u root -p < 002_seed.sql

USE taffy;

-- 密码均为 "password123" 的 bcrypt 哈希（cost=10）
INSERT INTO users (user_id, phone, email, password_hash, nickname) VALUES
(1, '13800000001', 'alice@example.com', '$2a$10$Fusz3BxVqfOiE70MUM8AEuVIWzkCNsWuFo2TqFrQ4HpCjj9cnhGgu', '小菲用户A'),
(2, '13800000002', 'bob@example.com',  '$2a$10$Fusz3BxVqfOiE70MUM8AEuVIWzkCNsWuFo2TqFrQ4HpCjj9cnhGgu', '小菲用户B');

-- 设备：每个用户各几个家具
--
-- 命名规范：
--   - speaker-* / light-* / aircon-* / curtain-* / socket-* 是按"类型-序号"的演示设备；
--   - dev1 / dev2 是专供 furniture/mock_furniture.py 默认参数（?device_id=dev1&token=t1）
--     直接连接使用的"开发期家具"，避免每个开发者都要改 mock 命令行。生产部署可删除。
INSERT INTO devices (device_id, owner_id, type, name, room, status, token) VALUES
('speaker-001', 1, 'SPEAKER', '客厅小菲',  '客厅', 'online',  'tk_speaker_001'),
('light-001',   1, 'LIGHT',   '客厅主灯',   '客厅', 'online',  'tk_light_001'),
('light-002',   1, 'LIGHT',   '卧室灯',     '卧室', 'offline', 'tk_light_002'),
('aircon-001',  1, 'AIRCON',  '客厅空调',    '客厅', 'online',  'tk_aircon_001'),
('curtain-001', 1, 'CURTAIN', '客厅窗帘',    '客厅', 'online',  'tk_curtain_001'),
('socket-001',  1, 'SOCKET',  '卧室插座',    '卧室', 'offline', 'tk_socket_001'),
('dev1',        1, 'SPEAKER', '开发用小菲',  '客厅', 'online',  't1'),

('speaker-002', 2, 'SPEAKER', '书房小菲',   '书房', 'online',  'tk_speaker_002'),
('light-003',   2, 'LIGHT',   '书房灯',     '书房', 'online',  'tk_light_003'),
('aircon-002',   2, 'AIRCON',  '书房空调',    '书房', 'offline', 'tk_aircon_002'),
('dev2',        2, 'SPEAKER', '开发用小菲2', '书房', 'online',  't2');

-- 设备状态
INSERT INTO device_states (device_id, power, brightness, temperature, mode, position) VALUES
('speaker-001', TRUE,  NULL, NULL,  NULL, NULL),
('light-001',   TRUE,  80,   NULL,  NULL, NULL),
('light-002',   FALSE, 0,    NULL,  NULL, NULL),
('aircon-001',  TRUE,  NULL, 26,    'cool', NULL),
('curtain-001', TRUE,  NULL, NULL,  NULL, 70),
('socket-001',  FALSE, NULL, NULL,  NULL, NULL),
('dev1',        TRUE,  NULL, NULL,  NULL, NULL),

('speaker-002', TRUE,  NULL, NULL,  NULL, NULL),
('light-003',   TRUE,  60,   NULL,  NULL, NULL),
('aircon-002',  FALSE, NULL, 24,    'auto', NULL),
('dev2',        TRUE,  NULL, NULL,  NULL, NULL);

-- 场景（command_list 每项字段平铺，与 control_device params 结构一致）
INSERT INTO scenes (user_id, name, command_list) VALUES
(1, '回家模式', '[{"device_id":"light-001","action":"turn_on","brightness":80},{"device_id":"aircon-001","action":"turn_on","temperature":26,"mode":"cool"},{"device_id":"curtain-001","action":"set_position","position":70}]'),
(1, '睡眠模式', '[{"device_id":"light-001","action":"turn_off"},{"device_id":"light-002","action":"turn_off"},{"device_id":"curtain-001","action":"set_position","position":0},{"device_id":"aircon-001","action":"set_temperature","temperature":25,"mode":"auto"}]');
