-- Profiles saved by the Go app kept proxyPassword and totpSecret inside ssh_config. They live in
-- the keychain or vault now and are dropped on read, but the old JSON stayed in the column until
-- the profile was next saved. Remove them (the store also VACUUMs after this migration so the
-- old pages don't linger in the file).
UPDATE profiles
SET ssh_config = json_remove(ssh_config, '$.proxyPassword', '$.totpSecret')
WHERE json_valid(ssh_config)
  AND (json_extract(ssh_config, '$.proxyPassword') IS NOT NULL OR json_extract(ssh_config, '$.totpSecret') IS NOT NULL);

INSERT INTO schema_version (version, applied_at) VALUES (9, datetime('now'));
