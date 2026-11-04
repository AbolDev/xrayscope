package store

// schemaV3 adds the minutes in which users really moved data. The access log
// only records when connections open, so these minutes are what tells a long
// video from silence.
var schemaV3 = []string{
	`CREATE TABLE IF NOT EXISTS tact (
		hour    INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		node_id INTEGER NOT NULL DEFAULT 0,
		mask    INTEGER NOT NULL,
		PRIMARY KEY (hour, user_id, node_id)
	) WITHOUT ROWID`,
}
