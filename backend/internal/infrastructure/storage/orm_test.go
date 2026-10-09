package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// These fixtures deliberately use SQL DDL rather than migrations or AutoMigrate,
// so the assertions check ORM compatibility independently of schema management.
type ormFixtureRow struct {
	ID       int64   `gorm:"column:id;primaryKey"`
	Text     string  `gorm:"column:text"`
	Enabled  bool    `gorm:"column:enabled"`
	Count    int     `gorm:"column:count"`
	Optional *string `gorm:"column:optional"`
}

func (ormFixtureRow) TableName() string { return "orm_fixture_rows" }

func openORMFixture(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := Open(ctx, filepath.Join(t.TempDir(), "orm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, ctx
}

func createORMFixtureTable(t *testing.T, db *DB, ctx context.Context) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `CREATE TABLE orm_fixture_rows (
		id INTEGER PRIMARY KEY,
		text TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		count INTEGER NOT NULL,
		optional TEXT
	)`); err != nil {
		t.Fatal(err)
	}
}

func TestORMReusesModerncConnectionAndPragmas(t *testing.T) {
	db, ctx := openORMFixture(t)
	orm := db.ORM(ctx)
	pool, err := orm.DB()
	if err != nil {
		t.Fatal(err)
	}
	if pool != db.DB {
		t.Fatal("ORM opened a different database/sql connection pool")
	}
	if driver := reflect.TypeOf(pool.Driver()).Elem().PkgPath(); driver != "modernc.org/sqlite" {
		t.Fatalf("expected modernc driver, got %s", driver)
	}
	if orm.Statement.Context != ctx {
		t.Fatal("ORM handle did not retain the supplied context")
	}
	if !orm.Config.SkipDefaultTransaction {
		t.Fatal("ordinary ORM writes must not start implicit transactions")
	}
	if got := pool.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("expected maximum of one open connection, got %d", got)
	}

	// Holding the only connection must prevent an ORM query from acquiring a
	// second one; cancellation also proves the query uses the supplied context.
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	var value int
	err = db.ORM(blockedCtx).Raw("SELECT 1").Scan(&value).Error
	cancel()
	if closeErr := conn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected single-connection wait to respect context, got %v", err)
	}

	for _, pragma := range []struct {
		name string
		want int
	}{
		{name: "foreign_keys", want: 1},
		{name: "busy_timeout", want: 5000},
	} {
		var got int
		if err := db.ORM(ctx).Raw("PRAGMA " + pragma.name).Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != pragma.want {
			t.Fatalf("PRAGMA %s: expected %d, got %d", pragma.name, pragma.want, got)
		}
	}
	var mode string
	if err := db.ORM(ctx).Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("expected WAL for a file database, got %q", mode)
	}
	stats := pool.Stats()
	if stats.OpenConnections != 1 || stats.Idle != 1 || stats.InUse != 0 {
		t.Fatalf("expected one idle connection after queries, got %+v", stats)
	}
}

func TestORMOpenCanceledContextReturnsNoDB(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "canceled.db"))
	if db != nil {
		_ = db.Close()
		t.Fatal("Open returned a DB after initialization failed")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected initialization to preserve context cancellation, got %v", err)
	}
}

func TestORMCreateTakeAndExplicitZeroValueUpdates(t *testing.T) {
	db, ctx := openORMFixture(t)
	createORMFixtureTable(t, db, ctx)
	optional := "present"
	row := ormFixtureRow{Text: "original", Enabled: true, Count: 7, Optional: &optional}
	if result := db.ORM(ctx).Create(&row); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("Create: rows=%d error=%v", result.RowsAffected, result.Error)
	}
	if row.ID == 0 {
		t.Fatal("Create did not return the generated primary key")
	}
	var got ormFixtureRow
	if err := db.ORM(ctx).Where("id = ?", row.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, row) {
		t.Fatalf("Take: expected %+v, got %+v", row, got)
	}
	if err := db.ORM(ctx).Where("id = ?", row.ID+1).Take(&ormFixtureRow{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected missing Take to return ErrRecordNotFound, got %v", err)
	}

	result := db.ORM(ctx).Model(&ormFixtureRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"text": "", "enabled": false, "count": 0, "optional": nil,
	})
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("Updates: rows=%d error=%v", result.RowsAffected, result.Error)
	}
	var text string
	var enabled, count int
	var nullable sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT text, enabled, count, optional FROM orm_fixture_rows WHERE id = ?", row.ID).Scan(&text, &enabled, &count, &nullable); err != nil {
		t.Fatal(err)
	}
	if text != "" || enabled != 0 || count != 0 || nullable.Valid {
		t.Fatalf("zero values were not persisted: text=%q enabled=%d count=%d optional=%+v", text, enabled, count, nullable)
	}

	// Select makes zero-valued struct updates explicit as well.
	if err := db.ORM(ctx).Model(&ormFixtureRow{}).Where("id = ?", row.ID).
		Updates(map[string]any{"text": "reset", "enabled": true, "count": 9}).Error; err != nil {
		t.Fatal(err)
	}
	result = db.ORM(ctx).Model(&ormFixtureRow{}).Where("id = ?", row.ID).
		Select("text", "enabled", "count", "optional").Updates(ormFixtureRow{})
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("selected struct Updates: rows=%d error=%v", result.RowsAffected, result.Error)
	}
	got = ormFixtureRow{}
	if err := db.ORM(ctx).Where("id = ?", row.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || got.Enabled || got.Count != 0 || got.Optional != nil {
		t.Fatalf("selected zero values were not persisted: %+v", got)
	}
	result = db.ORM(ctx).Model(&ormFixtureRow{}).Where("id = ?", row.ID+1).Updates(map[string]any{"text": "missing"})
	if result.Error != nil || result.RowsAffected != 0 {
		t.Fatalf("missing update: rows=%d error=%v", result.RowsAffected, result.Error)
	}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orm_fixture_rows").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("missing update inserted a row: total=%d", total)
	}
}

func TestORMReturningAndTriggers(t *testing.T) {
	db, ctx := openORMFixture(t)
	createORMFixtureTable(t, db, ctx)
	for _, ddl := range []string{
		"CREATE TABLE orm_fixture_audit (row_id INTEGER NOT NULL, text TEXT NOT NULL)",
		`CREATE TRIGGER orm_fixture_insert_audit AFTER INSERT ON orm_fixture_rows
		BEGIN INSERT INTO orm_fixture_audit(row_id, text) VALUES (NEW.id, NEW.text); END`,
		`CREATE TRIGGER orm_fixture_reject_update BEFORE UPDATE ON orm_fixture_rows
		WHEN NEW.text = 'forbidden'
		BEGIN SELECT RAISE(ABORT, 'orm fixture update rejected'); END`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	row := ormFixtureRow{Text: "created", Enabled: true, Count: 3}
	result := db.ORM(ctx).Clauses(clause.Returning{}).Create(&row)
	if result.Error != nil || result.RowsAffected != 1 || row.ID == 0 {
		t.Fatalf("Create RETURNING: row=%+v rows=%d error=%v", row, result.RowsAffected, result.Error)
	}
	var auditID int64
	var auditText string
	if err := db.QueryRowContext(ctx, "SELECT row_id, text FROM orm_fixture_audit").Scan(&auditID, &auditText); err != nil {
		t.Fatal(err)
	}
	if auditID != row.ID || auditText != "created" {
		t.Fatalf("INSERT trigger did not observe ORM write: id=%d text=%q", auditID, auditText)
	}

	var returned []ormFixtureRow
	result = db.ORM(ctx).Model(&returned).Clauses(clause.Returning{}).Where("id = ?", row.ID).
		Updates(map[string]any{"text": "updated", "count": gorm.Expr("count + 1")})
	if result.Error != nil || result.RowsAffected != 1 || len(returned) != 1 {
		t.Fatalf("UPDATE RETURNING: rows=%d returned=%+v error=%v", result.RowsAffected, returned, result.Error)
	}
	if returned[0].ID != row.ID || returned[0].Text != "updated" || returned[0].Count != 4 || !returned[0].Enabled {
		t.Fatalf("UPDATE RETURNING did not contain persisted values: %+v", returned[0])
	}
	if err := db.ORM(ctx).Model(&ormFixtureRow{}).Where("id = ?", row.ID).
		Updates(map[string]any{"text": "forbidden"}).Error; err == nil {
		t.Fatal("ORM update bypassed the rejecting trigger")
	}
	var text string
	if err := db.QueryRowContext(ctx, "SELECT text FROM orm_fixture_rows WHERE id = ?", row.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "updated" {
		t.Fatalf("rejected update changed persisted data: %q", text)
	}
}

func TestWithinTxCommitErrorAndPanic(t *testing.T) {
	for _, outcome := range []string{"commit", "callback error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			db, ctx := openORMFixture(t)
			createORMFixtureTable(t, db, ctx)
			sentinel := errors.New("callback failure")
			panicValue := &struct{ message string }{message: "callback panic"}
			var recovered any
			var err error
			func() {
				defer func() { recovered = recover() }()
				err = db.WithinTx(ctx, func(txCtx context.Context, tx *gorm.DB) error {
					if txCtx != ctx || tx.Statement.Context != ctx {
						return errors.New("transaction did not preserve callback and ORM contexts")
					}
					if _, ok := tx.Statement.ConnPool.(*sql.Tx); !ok {
						return errors.New("callback received an ORM handle without a database/sql transaction")
					}
					for _, text := range []string{"first", "second"} {
						if err := tx.Create(&ormFixtureRow{Text: text}).Error; err != nil {
							return err
						}
					}
					switch outcome {
					case "callback error":
						return sentinel
					case "panic":
						panic(panicValue)
					default:
						return nil
					}
				})
			}()
			wantRows := 0
			switch outcome {
			case "commit":
				if err != nil || recovered != nil {
					t.Fatalf("successful transaction: error=%v panic=%v", err, recovered)
				}
				wantRows = 2
			case "callback error":
				if !errors.Is(err, sentinel) || recovered != nil {
					t.Fatalf("callback error was not preserved: error=%v panic=%v", err, recovered)
				}
			case "panic":
				if recovered != panicValue {
					t.Fatalf("panic was not propagated: got %v", recovered)
				}
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orm_fixture_rows").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != wantRows {
				t.Fatalf("expected %d committed rows, got %d", wantRows, count)
			}
			// A fresh write proves rollback released the sole connection.
			if err := db.ORM(ctx).Create(&ormFixtureRow{Text: "after transaction"}).Error; err != nil {
				t.Fatalf("connection unusable after %s: %v", outcome, err)
			}
		})
	}
}

func TestWithinTxDeferredForeignKeyCommitFailureRollsBack(t *testing.T) {
	db, ctx := openORMFixture(t)
	createORMFixtureTable(t, db, ctx)
	for _, ddl := range []string{
		"CREATE TABLE orm_fixture_parents (id INTEGER PRIMARY KEY)",
		`CREATE TABLE orm_fixture_children (
			id INTEGER PRIMARY KEY,
			parent_id INTEGER NOT NULL REFERENCES orm_fixture_parents(id) DEFERRABLE INITIALLY DEFERRED
		)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	callbackSucceeded := false
	err := db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		if err := tx.Create(&ormFixtureRow{Text: "must roll back"}).Error; err != nil {
			return err
		}
		if err := tx.Table("orm_fixture_children").Create(map[string]any{"id": 1, "parent_id": 404}).Error; err != nil {
			return err
		}
		callbackSucceeded = true
		return nil
	})
	if !callbackSucceeded {
		t.Fatalf("fixture failed before COMMIT instead of exercising a deferred FK: %v", err)
	}
	if err == nil {
		t.Fatal("expected COMMIT to fail for the deferred foreign key")
	}
	for _, table := range []string{"orm_fixture_rows", "orm_fixture_children", "orm_fixture_parents"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("COMMIT failure left %d rows in %s", count, table)
		}
	}
	if err := db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		return tx.Create(&ormFixtureRow{Text: "after failed commit"}).Error
	}); err != nil {
		t.Fatalf("connection unusable after failed COMMIT: %v", err)
	}
}

func TestORMSilentLoggerDoesNotFormatSQLOnErrorOrSlowQuery(t *testing.T) {
	db, ctx := openORMFixture(t)
	productionLogger := db.ORM(ctx).Config.Logger
	// Inspect the actual configured GORM logger, rather than constructing a
	// separate Silent logger that could conceal a production configuration bug.
	config := reflect.Indirect(reflect.ValueOf(productionLogger)).FieldByName("Config")
	if !config.IsValid() || config.Type() != reflect.TypeOf(logger.Config{}) {
		t.Fatalf("expected production GORM logger configuration, got %T", productionLogger)
	}
	loggerConfig := config.Interface().(logger.Config)
	if loggerConfig.LogLevel != logger.Silent {
		t.Fatalf("expected Silent production logger, got level %v", loggerConfig.LogLevel)
	}
	if loggerConfig.SlowThreshold <= 0 {
		t.Fatal("fixture requires a configured slow query threshold")
	}
	const secret = "orm-secret-message-and-tool-parameter"
	for _, scenario := range []struct {
		name  string
		begin time.Time
		err   error
	}{
		{name: "error", begin: time.Now(), err: errors.New(secret)},
		{name: "record not found", begin: time.Now(), err: gorm.ErrRecordNotFound},
		{name: "slow query", begin: time.Now().Add(-loggerConfig.SlowThreshold - time.Second)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			formatted := false
			productionLogger.Trace(ctx, scenario.begin, func() (string, int64) {
				formatted = true
				return "SELECT '" + secret + "'", 1
			}, scenario.err)
			if formatted {
				t.Fatal("Silent production logger evaluated SQL containing sensitive parameters")
			}
		})
	}
}
