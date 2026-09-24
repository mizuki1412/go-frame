-- =============================================================
-- go-frame user 模块 DDL — MySQL 8.0+
-- 表：sys_privilege_constant / sys_department / sys_role /
--     sys_user / sys_user_role
-- 约定：
--   * 逻辑删除列 deleted（0 未删 / 1 已删），DAO 侧 logicDel 自动过滤
--   * 用户删除时 username/phone 置 NULL，规避唯一索引冲突
--   * 时间列 datetime(3)，与 Go 侧 class.Time 对应
-- =============================================================

-- ---------- 权限码字典 ----------
CREATE TABLE IF NOT EXISTS `sys_privilege_constant` (
  `id`   varchar(128) NOT NULL COMMENT '权限码',
  `name` varchar(128) NOT NULL DEFAULT '' COMMENT '权限名称',
  `type` varchar(64)  NOT NULL DEFAULT '' COMMENT '权限分组，暂不用',
  `sort` int          NOT NULL DEFAULT 0 COMMENT '排序，小在前',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='权限码字典';

-- ---------- 部门（树形） ----------
CREATE TABLE IF NOT EXISTS `sys_department` (
  `id`          bigint       NOT NULL AUTO_INCREMENT,
  `no`          varchar(64)  NOT NULL DEFAULT '' COMMENT '编号',
  `name`        varchar(64)  NOT NULL DEFAULT '' COMMENT '部门名称',
  `description` varchar(512) NOT NULL DEFAULT '' COMMENT '描述',
  `parent`      bigint       NOT NULL DEFAULT 0 COMMENT '父部门id，0 为根',
  `immutable`   tinyint(1)   NOT NULL DEFAULT 0 COMMENT '内置部门，不可删除',
  `extend`      json                  DEFAULT NULL COMMENT '扩展字段',
  `deleted`     tinyint(1)   NOT NULL DEFAULT 0 COMMENT '逻辑删除 0未删 1已删',
  `createdt`    datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updatedt`    datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_department_parent` (`parent`),
  KEY `idx_department_no` (`no`),
  CONSTRAINT `fk_department_parent` FOREIGN KEY (`parent`) REFERENCES `sys_department` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='部门';

-- ---------- 角色（全局，不绑部门；数据范围只由用户部门决定） ----------
CREATE TABLE IF NOT EXISTS `sys_role` (
  `id`          bigint       NOT NULL AUTO_INCREMENT COMMENT 'id=0 保留给内置超级管理员',
  `name`        varchar(64)  NOT NULL DEFAULT '' COMMENT '角色名称',
  `description` varchar(512) NOT NULL DEFAULT '' COMMENT '描述',
  `privileges`  json                  DEFAULT NULL COMMENT '权限码数组，如 ["user:add"]',
  `immutable`   tinyint(1)   NOT NULL DEFAULT 0 COMMENT '内置角色，不可删除',
  `extend`      json                  DEFAULT NULL COMMENT '扩展字段',
  `deleted`     tinyint(1)   NOT NULL DEFAULT 0 COMMENT '逻辑删除 0未删 1已删',
  `createdt`    datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updatedt`    datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='角色';

-- ---------- 用户 ----------
CREATE TABLE IF NOT EXISTS `sys_user` (
  `id`         bigint       NOT NULL AUTO_INCREMENT,
  `department` bigint                DEFAULT NULL COMMENT '部门id',
  `username`   varchar(64)           DEFAULT NULL COMMENT '登录名，删除后置NULL',
  `name`       varchar(64)  NOT NULL DEFAULT '' COMMENT '真实姓名',
  `phone`      varchar(32)           DEFAULT NULL COMMENT '手机号，删除后置NULL',
  `pwd`        varchar(255) NOT NULL DEFAULT '' COMMENT 'bcrypt 密码哈希',
  `gender`     int          NOT NULL DEFAULT 0 COMMENT '1-男 2-女',
  `image`      varchar(1024) NOT NULL DEFAULT '' COMMENT '头像',
  `address`    varchar(512) NOT NULL DEFAULT '' COMMENT '地址',
  `status`     int          NOT NULL DEFAULT 0 COMMENT '0正常 1冻结',
  `immutable`  tinyint(1)   NOT NULL DEFAULT 0 COMMENT '内置用户，不可删除/改角色',
  `extend`     json                  DEFAULT NULL COMMENT '扩展字段，如 privilegeExclude 权限剔除',
  `deleted`    tinyint(1)   NOT NULL DEFAULT 0 COMMENT '逻辑删除 0未删 1已删',
  `createdt`   datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updatedt`   datetime(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  -- 逻辑删除后 username/phone 被置 NULL，MySQL 唯一索引允许多行 NULL
  UNIQUE KEY `uk_user_username` (`username`),
  UNIQUE KEY `uk_user_phone` (`phone`),
  KEY `idx_user_department` (`department`),
  KEY `idx_user_name` (`name`),
  CONSTRAINT `fk_user_department` FOREIGN KEY (`department`) REFERENCES `sys_department` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='用户';

-- ---------- 用户-角色关联（多对多中间表） ----------
CREATE TABLE IF NOT EXISTS `sys_user_role` (
  `userid`   bigint      NOT NULL COMMENT '用户id',
  `roleid`   bigint      NOT NULL COMMENT '角色id',
  `createdt` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`userid`, `roleid`),
  KEY `idx_user_role_roleid` (`roleid`),
  CONSTRAINT `fk_user_role_user` FOREIGN KEY (`userid`) REFERENCES `sys_user` (`id`),
  CONSTRAINT `fk_user_role_role` FOREIGN KEY (`roleid`) REFERENCES `sys_role` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='用户-角色关联';

-- =============================================================
-- 种子数据
-- =============================================================

-- ---------- 权限码字典 ----------
-- 与 mod/user/model/privilege.go 的权限码常量一一对应；
-- middleware.AuthPerm 消费这些码，sys_role.privileges 授予这些码。
INSERT INTO `sys_privilege_constant` (`id`, `name`, `type`, `sort`) VALUES
  ('user:admin',        '用户管理',     'user',       10),
  ('user:session',      '在线会话管理', 'user',       20),
  ('role:manage',       '角色管理',     'role',       30),
  ('department:manage', '部门管理',     'department', 40)
ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `type` = VALUES(`type`), `sort` = VALUES(`sort`);

-- ---------- 内置超级管理员角色（id=0） ----------
-- 权限码 "*" 表示通配一切（tokenkit.Principal.HasPrivilege 的匹配规则）。
-- 持有者受保护：不允许经管理员接口改绑或删除（service 层已拦截）。
INSERT INTO `sys_role` (`id`, `name`, `description`, `privileges`, `immutable`, `extend`)
VALUES (0, '超级管理员', '内置角色，权限码 * 表示全部权限', JSON_ARRAY('*'), 1, JSON_OBJECT())
ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `description` = VALUES(`description`);
