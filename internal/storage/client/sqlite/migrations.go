package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	name  string
	stmts []string
}

// Версия схемы живёт в PRAGMA user_version: индекс последней применённой миграции,
// считая с единицы. Новую миграцию дописываем в конец, старые не трогаем: на боевой
// базе они уже отмечены применёнными и повторно не выполнятся.
var migrations = []migration{
	{
		name: "init",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS check_log (
    id INTEGER PRIMARY KEY,
    checked_at INTEGER NOT NULL DEFAULT (unixepoch()),
    url_hash TEXT NOT NULL,
    item_name TEXT NOT NULL DEFAULT '',
    item_url TEXT NOT NULL DEFAULT '',
    tracker_type_id INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    error_streak INTEGER NOT NULL DEFAULT 0
)`,
			`CREATE TABLE IF NOT EXISTS "ignore" (url_hash TEXT PRIMARY KEY, item_name TEXT NOT NULL DEFAULT '', item_url TEXT NOT NULL DEFAULT '', added_at INTEGER NOT NULL DEFAULT (unixepoch()))`,
			`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL DEFAULT (unixepoch()))`,
			`CREATE TABLE IF NOT EXISTS trackers (tracker_type_id INTEGER PRIMARY KEY, tracker_name TEXT, domain_name TEXT, login_url TEXT, download_url TEXT, credentials TEXT, cookies TEXT, publicity TEXT, updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS history (url_hash TEXT PRIMARY KEY, tracker_type_id INTEGER, topic_id TEXT, category_name TEXT, item_tag TEXT, item_name TEXT, item_url TEXT, created_at INTEGER, recheck_at INTEGER, force_check INTEGER NOT NULL DEFAULT 0)`,
			`CREATE INDEX IF NOT EXISTS check_log_checked_at_idx ON check_log (checked_at DESC)`,
			`DELETE FROM check_log
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY url_hash ORDER BY checked_at DESC, id DESC) AS rn
        FROM check_log
    ) dedup WHERE rn > 1
)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS check_log_url_hash_uidx ON check_log (url_hash)`,
			`DELETE FROM check_log
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY LOWER(TRIM(item_name)) ORDER BY checked_at DESC, id DESC) AS rn
        FROM check_log
    ) dedup_name WHERE rn > 1
)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS check_log_item_name_uidx ON check_log (LOWER(TRIM(item_name))) WHERE TRIM(item_name) <> ''`,
		},
	},
}

func (c *Client) Bootstrap(ctx context.Context) error {
	return c.migrate(ctx, migrations)
}

func (c *Client) migrate(ctx context.Context, ms []migration) error {
	var version int
	if err := c.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(ms) {
		// база от более новой сборки: её схему этот бинарник не понимает, а накат
		// вниз ничего не откатит, поэтому падаем, а не портим данные молча
		return fmt.Errorf("schema version %d is newer than this build knows (%d)", version, len(ms))
	}
	for i := version; i < len(ms); i++ {
		if err := c.applyMigration(ctx, i+1, ms[i]); err != nil {
			return fmt.Errorf("migration %d (%s): %w", i+1, ms[i].name, err)
		}
	}
	return nil
}

func (c *Client) applyMigration(ctx context.Context, version int, m migration) (err error) {
	var tx *sql.Tx
	if tx, err = c.db.BeginTx(ctx, nil); err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	// версия перечитывается уже внутри транзакции: DSN просит BEGIN IMMEDIATE,
	// поэтому пишущая блокировка взята до этого чтения, и второй процесс, стартовавший
	// на том же файле, увидит здесь версию, которую первый успел закоммитить, пока
	// он ждал блокировку. Без перечитывания оба накатили бы один шаг, и второму
	// любой будущий ALTER TABLE ADD COLUMN вернул бы duplicate column
	var current int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return
	}
	if current >= version {
		err = tx.Commit()
		return
	}
	for i, stmt := range m.stmts {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			err = fmt.Errorf("statement %d: %w", i, err)
			return
		}
	}
	// user_version не принимает плейсхолдер, поэтому число подставляется в текст;
	// источник у него один, счётчик цикла, так что подставлять нечему
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return
	}
	err = tx.Commit()
	return
}
