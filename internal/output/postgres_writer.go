package output

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/genjerator/krile/internal/models"
)

type PostgresWriter struct {
	pool           *pgxpool.Pool
	tableName      string
	ctx            context.Context
	usesUUID       bool
	hasUpdated     bool
	updateExisting bool
	debug          bool
}

func NewPostgresWriter(ctx context.Context, connString, tableName string, updateExisting, debug bool) (*PostgresWriter, error) {
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
		pool:           pool,
		tableName:      tableName,
		ctx:            ctx,
		updateExisting: updateExisting,
		debug:          debug,
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DB-DEBUG] Connected to database, table: %s\n", tableName)
		fmt.Fprintf(os.Stderr, "[DB-DEBUG] Update mode: %v\n", updateExisting)
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

	if debug {
		fmt.Fprintf(os.Stderr, "[DB-DEBUG] Schema detected - UUID: %v, Has updated_at: %v\n", w.usesUUID, w.hasUpdated)
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

// nullIfEmpty returns nil if the string is empty, otherwise returns the string
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (w *PostgresWriter) isDuplicate(b models.Business) (bool, error) {
	// In update mode, check by source_url
	if w.updateExisting {
		var exists bool
		query := fmt.Sprintf(`
			SELECT EXISTS (
				SELECT 1 FROM %s
				WHERE source_url = $1
			)
		`, pgx.Identifier{w.tableName}.Sanitize())
		err := w.pool.QueryRow(w.ctx, query, b.SourceURL).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("check source_url duplicate: %w", err)
		}
		return exists, nil
	}

	// In normal mode, check by email or name
	var exists bool
	if b.Email != "" {
		// If email is not empty, check if email already exists
		query := fmt.Sprintf(`
			SELECT EXISTS (
				SELECT 1 FROM %s
				WHERE email = $1 AND email IS NOT NULL AND email <> ''
			)
		`, pgx.Identifier{w.tableName}.Sanitize())
		err := w.pool.QueryRow(w.ctx, query, b.Email).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("check email duplicate: %w", err)
		}
	} else {
		// If email is empty, check if name exists (where email is also empty)
		query := fmt.Sprintf(`
			SELECT EXISTS (
				SELECT 1 FROM %s
				WHERE name = $1 AND (email IS NULL OR email = '')
			)
		`, pgx.Identifier{w.tableName}.Sanitize())
		err := w.pool.QueryRow(w.ctx, query, b.Name).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("check name duplicate: %w", err)
		}
	}
	return exists, nil
}

func (w *PostgresWriter) Write(businesses []models.Business) (int, error) {
	if len(businesses) == 0 {
		return 0, nil
	}

	if w.debug {
		fmt.Fprintf(os.Stderr, "[DB-DEBUG] Processing batch of %d businesses\n", len(businesses))
	}

	// Build INSERT query based on detected schema
	var insertQuery string
	if w.usesUUID && w.hasUpdated {
		// UUID-based table with updated_at (like companies table)
		insertQuery = fmt.Sprintf(`
			INSERT INTO %s (id, name, category, street, postal_code, city, phone, website, email, source_url, scraped_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		`, pgx.Identifier{w.tableName}.Sanitize())
	} else if w.hasUpdated {
		// SERIAL-based table with updated_at
		insertQuery = fmt.Sprintf(`
			INSERT INTO %s (name, category, street, postal_code, city, phone, website, email, source_url, scraped_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		`, pgx.Identifier{w.tableName}.Sanitize())
	} else {
		// SERIAL-based table without updated_at (default)
		insertQuery = fmt.Sprintf(`
			INSERT INTO %s (name, category, street, postal_code, city, phone, website, email, source_url, scraped_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, pgx.Identifier{w.tableName}.Sanitize())
	}

	// Build UPDATE query for update mode
	var updateQuery string
	if w.updateExisting {
		if w.hasUpdated {
			updateQuery = fmt.Sprintf(`
				UPDATE %s SET
					name = $1,
					category = $2,
					street = $3,
					postal_code = $4,
					city = $5,
					phone = $6,
					website = $7,
					email = $8,
					scraped_at = $9,
					updated_at = NOW()
				WHERE source_url = $10
			`, pgx.Identifier{w.tableName}.Sanitize())
		} else {
			updateQuery = fmt.Sprintf(`
				UPDATE %s SET
					name = $1,
					category = $2,
					street = $3,
					postal_code = $4,
					city = $5,
					phone = $6,
					website = $7,
					email = $8,
					scraped_at = $9
				WHERE source_url = $10
			`, pgx.Identifier{w.tableName}.Sanitize())
		}
	}

	// Use batch insert/update for better performance
	batch := &pgx.Batch{}
	skipped := 0
	updated := 0

	// Track seen items in this batch
	seenSourceURLs := make(map[string]bool)
	seenEmails := make(map[string]bool)
	seenNames := make(map[string]bool)

	for _, b := range businesses {
		if w.debug {
			fmt.Fprintf(os.Stderr, "[DB-DEBUG] Checking: %s | Email: %s | Source: %s\n", b.Name, b.Email, b.SourceURL)
		}

		// Check for duplicates in database
		isDup, err := w.isDuplicate(b)
		if err != nil {
			return 0, fmt.Errorf("check duplicate for %s: %w", b.Name, err)
		}

		if isDup {
			if w.updateExisting {
				// Update existing record
				fmt.Fprintf(os.Stderr, "[UPDATE] Updating existing record: %s\n", b.Name)
				if w.debug {
					fmt.Fprintf(os.Stderr, "[DB-DEBUG] Update data - Email: %s | Phone: %s | Website: %s\n", b.Email, b.Phone, b.Website)
				}
				batch.Queue(updateQuery,
					b.Name,
					b.Category,
					b.Street,
					b.PostalCode,
					b.City,
					b.Phone,
					b.Website,
					nullIfEmpty(b.Email),
					b.ScrapedAt,
					b.SourceURL,
				)
				updated++
			} else {
				// Skip duplicate
				if b.Email != "" {
					fmt.Fprintf(os.Stderr, "[SKIP] Duplicate email: %s (%s)\n", b.Email, b.Name)
				} else {
					fmt.Fprintf(os.Stderr, "[SKIP] Duplicate name: %s\n", b.Name)
				}
				skipped++
			}
			continue
		}

		// Check for duplicates within current batch
		if w.updateExisting {
			if seenSourceURLs[b.SourceURL] {
				fmt.Fprintf(os.Stderr, "[SKIP] Duplicate source_url in batch: %s\n", b.Name)
				skipped++
				continue
			}
			seenSourceURLs[b.SourceURL] = true
		} else {
			if b.Email != "" {
				if seenEmails[b.Email] {
					fmt.Fprintf(os.Stderr, "[SKIP] Duplicate email in batch: %s (%s)\n", b.Email, b.Name)
					skipped++
					continue
				}
				seenEmails[b.Email] = true
			} else {
				if seenNames[b.Name] {
					fmt.Fprintf(os.Stderr, "[SKIP] Duplicate name in batch: %s\n", b.Name)
					skipped++
					continue
				}
				seenNames[b.Name] = true
			}
		}

		// Insert new record
		if w.debug {
			fmt.Fprintf(os.Stderr, "[DB-DEBUG] Inserting: %s | Email: %s | Phone: %s | Website: %s\n", b.Name, b.Email, b.Phone, b.Website)
		}

		if w.usesUUID && w.hasUpdated {
			// Generate UUID for companies table
			newUUID := uuid.New().String()
			if w.debug {
				fmt.Fprintf(os.Stderr, "[DB-DEBUG] Generated UUID: %s\n", newUUID)
			}
			batch.Queue(insertQuery,
				newUUID,
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				nullIfEmpty(b.Email),
				b.SourceURL,
				b.ScrapedAt,
			)
		} else if w.hasUpdated {
			batch.Queue(insertQuery,
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				nullIfEmpty(b.Email),
				b.SourceURL,
				b.ScrapedAt,
			)
		} else {
			batch.Queue(insertQuery,
				b.Name,
				b.Category,
				b.Street,
				b.PostalCode,
				b.City,
				b.Phone,
				b.Website,
				nullIfEmpty(b.Email),
				b.SourceURL,
				b.ScrapedAt,
			)
		}
	}

	totalOps := batch.Len()

	if w.debug {
		fmt.Fprintf(os.Stderr, "[DB-DEBUG] Executing batch with %d operations (inserts + updates)\n", totalOps)
	}

	// Only send batch if we have operations
	if totalOps > 0 {
		br := w.pool.SendBatch(w.ctx, batch)
		defer br.Close()

		// Execute all queries in batch
		for i := 0; i < totalOps; i++ {
			_, err := br.Exec()
			if err != nil {
				if w.debug {
					fmt.Fprintf(os.Stderr, "[DB-DEBUG] Error at operation %d: %v\n", i, err)
				}
				return 0, fmt.Errorf("execute operation %d: %w", i, err)
			}
		}

		if w.debug {
			fmt.Fprintf(os.Stderr, "[DB-DEBUG] Batch executed successfully\n")
		}
	}

	// Return total operations count (inserts + updates)
	return totalOps, nil
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
