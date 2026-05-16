-- SHVA 数据库初始化脚本
-- 执行: mysql -u root -p < 001_init.sql

CREATE DATABASE IF NOT EXISTS shva
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_unicode_ci;

USE shva;

-- ============================================================
-- 1. 用户表
-- ============================================================
CREATE TABLE users (
    user_id       BIGINT AUTO_INCREMENT PRIMARY KEY,
    phone         VARCHAR(20) UNIQUE,
    email         VARCHAR(100) UNIQUE,
    password_hash VARCHAR(72) NOT NULL COMMENT 'bcrypt 哈希',
    nickname      VARCHAR(50) NOT NULL DEFAULT '',
    avatar_url    VARCHAR(500) DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_phone (phone),
    INDEX idx_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户表';

-- ============================================================
-- 2. 设备表
-- ============================================================
CREATE TABLE devices (
    device_id  VARCHAR(64) PRIMARY KEY COMMENT '设备唯一标识（MAC/UUID）',
    owner_id   BIGINT NOT NULL COMMENT '归属用户',
    type       ENUM('SPEAKER','LIGHT','AIRCON','CURTAIN','SOCKET') NOT NULL,
    name       VARCHAR(50) NOT NULL DEFAULT '' COMMENT '用户自定义名称，如"客厅灯"',
    room       VARCHAR(50) NOT NULL DEFAULT '' COMMENT '房间，如"客厅"',
    status     ENUM('unregistered','waiting_bind','online','offline','error')
               NOT NULL DEFAULT 'unregistered',
    token      VARCHAR(128) DEFAULT '' COMMENT '设备鉴权 token',
    last_seen  DATETIME DEFAULT NULL COMMENT '最后在线时间',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (owner_id) REFERENCES users(user_id) ON DELETE CASCADE,
    INDEX idx_owner (owner_id),
    INDEX idx_type (type),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备表';

-- ============================================================
-- 3. 设备状态表（保存各家具的使用情况：是否开启、温度等）
-- ============================================================
CREATE TABLE device_states (
    device_id   VARCHAR(64) PRIMARY KEY,
    power       BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否开启',
    -- 灯（LIGHT）
    brightness  INT DEFAULT NULL COMMENT '亮度 0~100',
    -- 空调（AIRCON）
    temperature INT DEFAULT NULL COMMENT '温度 16~30',
    mode        ENUM('cool','heat','auto','fan','dry') DEFAULT NULL COMMENT '空调模式',
    -- 窗帘（CURTAIN）
    position    INT DEFAULT NULL COMMENT '开合度 0~100',
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(device_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备状态表';

-- ============================================================
-- 4. 对话会话表
-- ============================================================
CREATE TABLE conversations (
    conversation_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id         BIGINT NOT NULL,
    device_id       VARCHAR(64) DEFAULT NULL COMMENT '通过哪个设备发起',
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
    FOREIGN KEY (device_id) REFERENCES devices(device_id) ON DELETE SET NULL,
    INDEX idx_user (user_id),
    INDEX idx_started (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='对话会话表';

-- ============================================================
-- 5. 消息表
-- ============================================================
CREATE TABLE messages (
    message_id      BIGINT AUTO_INCREMENT PRIMARY KEY,
    conversation_id BIGINT NOT NULL,
    role            ENUM('user','assistant','system') NOT NULL,
    content         TEXT NOT NULL,
    audio_url       VARCHAR(500) DEFAULT '' COMMENT '对应音频文件地址',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (conversation_id) REFERENCES conversations(conversation_id) ON DELETE CASCADE,
    INDEX idx_conv (conversation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息表';

-- ============================================================
-- 6. 指令表（LLM 产生的设备控制指令）
-- ============================================================
CREATE TABLE commands (
    command_id  BIGINT AUTO_INCREMENT PRIMARY KEY,
    message_id  BIGINT NOT NULL,
    device_id   VARCHAR(64) NOT NULL,
    action      VARCHAR(50) NOT NULL COMMENT 'turn_on / turn_off / set_temp 等',
    params      JSON DEFAULT NULL COMMENT '{"temp":26,"mode":"cool"}',
    result      ENUM('pending','success','failure','timeout') NOT NULL DEFAULT 'pending',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    executed_at DATETIME DEFAULT NULL,
    FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE,
    FOREIGN KEY (device_id) REFERENCES devices(device_id) ON DELETE CASCADE,
    INDEX idx_message (message_id),
    INDEX idx_device (device_id),
    INDEX idx_result (result)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='指令表';

-- ============================================================
-- 7. 场景表（一键触发多个设备指令）
-- ============================================================
CREATE TABLE scenes (
    scene_id    BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id     BIGINT NOT NULL,
    name        VARCHAR(50) NOT NULL COMMENT '如"回家模式""睡眠模式"',
    command_list JSON NOT NULL COMMENT '[{"device_id":"d1","action":"turn_on","params":{}}]',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
    INDEX idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='场景表';
