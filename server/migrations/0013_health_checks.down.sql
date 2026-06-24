DROP TABLE IF EXISTS device_health;

ALTER TABLE schedules
    DROP COLUMN IF EXISTS warning_exit_codes,
    DROP COLUMN IF EXISTS check_type;
