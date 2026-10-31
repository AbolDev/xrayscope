package store

// schemaV2 adds nodes that the main server contacts itself (pull mode) and
// the outbox a node keeps for them.
var schemaV2 = []string{
	`ALTER TABLE nodes ADD COLUMN mode TEXT NOT NULL DEFAULT 'push'`,
	`ALTER TABLE nodes ADD COLUMN url TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE nodes ADD COLUMN username TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE nodes ADD COLUMN password TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE nodes ADD COLUMN proxy TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE nodes ADD COLUMN insecure INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE nodes ADD COLUMN pull_seq INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE nodes ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS outbox (
		seq  INTEGER PRIMARY KEY AUTOINCREMENT,
		body BLOB NOT NULL
	)`,
}
