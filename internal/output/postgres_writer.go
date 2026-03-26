package output

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/genjerator/krile/internal/models"
)

type PostgresWriter struct {
	pool       *pgxpool.Pool
	tableName  string
	ctx        context.Context
	usesUUID   bool
	hasUpdated bool
}

func NewPostgresWriter(ctx context.Context, connString, tableName string) (*PostgresWriter, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	w := &PostgresWriter{
		pool:      pool,
		tableName: tableName,
		ctx:       ctx,
	}

	// Create table if not exists
	if err := w.ensureTable(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ensure table: %w", err)
	}

	// Detect table schema
	if err := w.detectSchema(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("detect schema: %w", err)
	}

	return w, nil
}

func (w *PostgresWriter) ensureTable() error {
	// Check if table exists - if it does, skip creation
	// This allows using existing tables with custom schemas (like UUID-based companies table)
	var exists bool
	err := w.pool.QueryRow(w.ctx,
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)",
		w.tableName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check table existence: %w", err)
	}

	if exists {
		// Table exists, don't recreate it
		return nil
	}

	// Create table only if it doesn't exist (fallback for new tables)
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT,
			street TEXT,
			postal_code TEXT,
			city TEXT,
			phone TEXT,
			website TEXT,
			email TEXT,
			source_url TEXT NOT NULL,
			scraped_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`, pgx.Identifier{w.tableName}.Sanitize())

	_, err = w.pool.Exec(w.ctx, query)
	return err
}

func (w *PostgresWriter) detectSchema() error {
	// Check if id column is UUID type
	var dataType string
	err := w.pool.QueryRow(w.ctx, `
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		AND table_name = $1
		AND column_name = 'id'
	`, w.tableName).Scan(&dataType)
	if err != nil {
		return fmt.Errorf("detect id type: %w", err)
	}
	w.usesUUID = (dataType == "uuid")

	// Check if updated_at column exists
	var hasUpdated bool
	err = w.pool.QueryRow(w.ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			AND table_name = $1
			AND column_name = 'updated_at'
		)
	`, w.tableName).Scan(&hasUpdated)
	if err != nil {
		return fmt.Errorf("detect updated_at column: %w", err)
	}
	w.hasUpdated = hasUpdated

	return nil
}

func (w *PostgresWriter) Write(businesses []models.Business) error {
	if len(businesses) == 0 {
		return nil
	}

	// Build INSERT query based on detected schema
	var query string
	if w.usesUUID && w.hasUpdated {
		// UUID-based table with updated_at (like companies table)
		query = fmt.Sprintf(`
			INSERT INTO %s (id, name, category, street, postal_code, city, phone, website, email, source_url, scraped_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		`, pgx.Identifier{w.tableName}.Sanitize())
	} else if w.hasUpdated {
		// SERIAL-based table with updated_at
		query = fmt.Sprintf(`
			INSERT INTO %s (name, category, street, postal_code, city, phone, website, email, source_url, scraped_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		`, pgx.Identifier{w.tableName}.Sanitize())
	} else {
		// SERIAL-based table without updated_at (default)
		query = fmt.Sprintf(`
			INSERT INTO %s (name, category, street, postal_code, city, phone, website, email, source_url, scraped_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, pgx.Identifier{w.tableName}.Sanitize())
	}

	// Use batch insert for better performance
	batch := &pgx.Batch{}

	for _, b := range businesses {
		if w.usesUUID && w.hasUpdated {
			// Generate UUID for companies table
			batch.Queue(query,
				uuid.New().String(),
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				b.Email,
				b.SourceURL,
				b.ScrapedAt,
			)
		} else if w.hasUpdated {
			batch.Queue(query,
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				b.Email,
				b.SourceURL,
				b.ScrapedAt,
			)
		} else {
			batch.Queue(query,
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				b.Email,
				b.SourceURL,
				b.ScrapedAt,
			)
		}
	}

	br := w.pool.SendBatch(w.ctx, batch)
	defer br.Close()

	// Execute all queries in batch
	for i := 0; i < len(businesses); i++ {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert business %d: %w", i, err)
		}
	}

	return nil
}

func (w *PostgresWriter) Flush() error {
	// No buffering, nothing to flush
	return nil
}

func (w *PostgresWriter) Close() error {
	if w.pool != nil {
		w.pool.Close()
	}
	return nil
}
