DROP FUNCTION worker_list_tenants();
ALTER TABLE jobs DROP COLUMN schedule_id;
DROP TABLE schedules;
DROP INDEX jobs_queued_expiry_idx;
ALTER TABLE jobs DROP COLUMN expires_at;
