CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL CHECK(version = 0));
INSERT INTO schema_version(version) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_version);
CREATE TABLE IF NOT EXISTS core_state(id INTEGER PRIMARY KEY CHECK(id = 1), body TEXT NOT NULL CHECK(json_valid(body)));
CREATE TABLE IF NOT EXISTS repo_update_journal(id TEXT PRIMARY KEY, body TEXT NOT NULL CHECK(json_valid(body)));
CREATE TABLE IF NOT EXISTS runtime_blockers(id TEXT PRIMARY KEY, body TEXT NOT NULL CHECK(json_valid(body)));
