package database

import (
	"context"
	"log"
	"time"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Database struct {
	DB *gorm.DB
}

func Connect(cfg config.Config) (*Database, error) {
	var db *gorm.DB
	var err error

	gormLogger := logger.Default.LogMode(logger.Warn)
	if cfg.AppEnv == "development" {
		gormLogger = logger.Default.LogMode(logger.Info)
	}

	for attempt := 1; attempt <= 5; attempt++ {
		db, err = gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{
			Logger: gormLogger,
		})
		if err == nil {
			sqlDB, sqlErr := db.DB()
			if sqlErr == nil && sqlDB.Ping() == nil {
				sqlDB.SetMaxOpenConns(25)
				sqlDB.SetMaxIdleConns(10)
				sqlDB.SetConnMaxLifetime(time.Hour)
				return &Database{DB: db}, nil
			}
			if sqlErr != nil {
				err = sqlErr
			}
		}

		log.Printf("database connection attempt %d failed: %v", attempt, err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}

	return nil, err
}

func (d *Database) AutoMigrate() error {
	if err := d.DB.AutoMigrate(
		&models.User{},
		&models.AuthSession{},
		&models.ChatSession{},
		&models.ChatMessage{},
		&models.Trip{},
		&models.Itinerary{},
		&models.Booking{},
		&models.Payment{},
		&models.AILog{},
		&models.ToolCall{},
	); err != nil {
		return err
	}

	return d.migrateLegacySlots()
}

// MigrateGuestChatSessions removes the legacy shared guest-user ownership from
// chat sessions. Anonymous ownership is now represented by NULL UserID, while
// authenticated sessions keep their existing owner. Existing sessions also
// receive an expiry so the cleanup path applies consistently after upgrade.
func (d *Database) MigrateGuestChatSessions(ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	seconds := ttl.Seconds()
	return d.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			UPDATE chat_sessions
			SET user_id = NULL
			WHERE user_id = (SELECT id FROM users WHERE email = 'guest@vero.local' LIMIT 1)
		`).Error; err != nil {
			return err
		}
		return tx.Exec(`
			UPDATE chat_sessions
			SET last_activity_at = COALESCE(last_activity_at, updated_at, created_at),
				expires_at = COALESCE(expires_at, COALESCE(last_activity_at, updated_at, created_at) + (? * INTERVAL '1 second'))
			WHERE expires_at IS NULL
		`, seconds).Error
	})
}

func (d *Database) migrateLegacySlots() error {
	if !d.DB.Migrator().HasColumn("trips", "slots") {
		return nil
	}

	return d.DB.Exec(`
		UPDATE trips
		SET adult_pax = slots
		WHERE slots > 0 AND adult_pax = 0 AND child_pax = 0
	`).Error
}

// Health checks DB connectivity. PingContext already honors ctx (returns on
// timeout/cancel), so it is called directly — no goroutine wrapper needed.
// SEC-32: the previous goroutine + select wrapper leaked the goroutine whenever
// ctx timed out, since the blocking PingContext kept running after Health
// returned. Callers (DatabaseHealth, Readiness) pass a ctx with a 3s deadline.
func (d *Database) Health(ctx context.Context) error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func (d *Database) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
