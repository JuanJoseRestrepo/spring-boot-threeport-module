package migrations

import (
	"context"
	"database/sql"
	"fmt"

	goose "github.com/pressly/goose/v3"

	v0 "spring-boot-threeport-module/pkg/api/v0"
)

func init() {
	goose.AddMigrationNoTxContext(Up000002, Down000002)
}

// Up000002 adds the application container's CPU and memory fields to
// SpringBootDefinition.
//
// AutoMigrate against the same type the init migration used: gorm compares the
// struct to the table and adds what is missing, so an installation that has
// already run 000001 gains the four columns and a fresh one is unaffected,
// having been created from the current struct in the first place.
//
// Editing 000001 instead would work on a fresh install and do nothing on an
// existing one, because goose records it as applied and never runs it again.
func Up000002(ctx context.Context, db *sql.DB) error {
	gormDb, err := getGormDbFromContext(ctx)
	if err != nil {
		return err
	}

	if err := gormDb.AutoMigrate(&v0.SpringBootDefinition{}); err != nil {
		return fmt.Errorf("could not run gorm AutoMigrate for spring boot definition resources: %w", err)
	}

	return nil
}

// Down000002 removes the four columns.
func Down000002(ctx context.Context, db *sql.DB) error {
	gormDb, err := getGormDbFromContext(ctx)
	if err != nil {
		return err
	}

	for _, column := range []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"} {
		if err := gormDb.Migrator().DropColumn(&v0.SpringBootDefinition{}, column); err != nil {
			return fmt.Errorf("could not drop column %s: %w", column, err)
		}
	}

	return nil
}
