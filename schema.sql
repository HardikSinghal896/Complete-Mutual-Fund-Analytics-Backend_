CREATE DATABASE IF NOT EXISTS mfmvp
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE mfmvp;

CREATE TABLE IF NOT EXISTS nav_data (
    scheme_code VARCHAR(20)  NOT NULL,
    date        DATE         NOT NULL,
    nav         DOUBLE       NOT NULL,
    PRIMARY KEY (scheme_code, date)
);

-- MySQL does not support CREATE INDEX IF NOT EXISTS.
-- This procedure checks before creating to make it safe to re-run.
DROP PROCEDURE IF EXISTS add_index_if_missing;
DELIMITER $$
CREATE PROCEDURE add_index_if_missing()
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM   information_schema.STATISTICS
        WHERE  table_schema = DATABASE()
          AND  table_name   = 'nav_data'
          AND  index_name   = 'idx_nav_data_scheme_code'
    ) THEN
        CREATE INDEX idx_nav_data_scheme_code ON nav_data (scheme_code);
    END IF;
END$$
DELIMITER ;
CALL add_index_if_missing();
DROP PROCEDURE IF EXISTS add_index_if_missing;

CREATE TABLE IF NOT EXISTS sync_state (
    scheme_code      VARCHAR(20) NOT NULL,
    last_synced_date DATE,
    last_status      VARCHAR(20),
    updated_at       TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
                                 ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (scheme_code)
);
