CREATE TABLE `test_table`
(
    `id`        bigint(20) NOT NULL AUTO_INCREMENT,
    `score`     bigint(20) NOT NULL,
    `create_at` timestamp  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `update_at` timestamp  NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_bin
  AUTO_INCREMENT = 30001;