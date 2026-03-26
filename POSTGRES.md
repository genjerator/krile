# PostgreSQL Output Guide

## Setup

### 1. Create .env file

Copy the example file and update with your credentials:

```bash
cp .env.example .env
```

Edit `.env`:
```env
DB_HOST=localhost
DB_PORT=5432
DB_NAME=krile
DB_USER=postgres
DB_PASSWORD=your_password
DB_TABLE=businesses
```

### 2. Create Database

```sql
CREATE DATABASE krile;
```

The table will be created automatically when you first run the scraper.

## Usage

### Basic Usage

```bash
./krile --query "Restaurant" --city "Berlin" --format postgres
```

### With Custom Table Name

```bash
./krile --query "Apotheke" --city "Frankfurt" --format postgres --db-table pharmacies
```

### Override Environment Variables

```bash
./krile --query "Hotel" --city "München" --format postgres \
  --db-host localhost \
  --db-port 5432 \
  --db-name mydb \
  --db-user myuser \
  --db-password mypass \
  --db-table hotels
```

### Limit Results

```bash
./krile --query "Restaurant" --city "Berlin" --limit 50 --format postgres --verbose
```

## Database Schema

The scraper will automatically create this table if it doesn't exist:

```sql
CREATE TABLE businesses (
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
);
```

## Querying Data

### Get all restaurants in Berlin

```sql
SELECT * FROM businesses
WHERE city = 'Berlin'
ORDER BY scraped_at DESC;
```

### Count by city

```sql
SELECT city, COUNT(*) as count
FROM businesses
GROUP BY city
ORDER BY count DESC;
```

### Find businesses with websites

```sql
SELECT name, website, phone
FROM businesses
WHERE website != ''
ORDER BY name;
```

### Recent scrapes

```sql
SELECT * FROM businesses
WHERE scraped_at > NOW() - INTERVAL '1 day'
ORDER BY scraped_at DESC;
```

## Tips

- **Batch inserts**: The writer uses PostgreSQL batch inserts for better performance
- **Duplicate entries**: Run the scraper multiple times without worrying about duplicates (unless you want to track changes over time)
- **Connection pooling**: The writer uses connection pooling for efficient database usage
- **Auto-reconnect**: If the database connection is lost, the scraper will fail gracefully

## Troubleshooting

### "connection refused"
- Check if PostgreSQL is running: `pg_isready`
- Verify host and port in .env

### "authentication failed"
- Check DB_USER and DB_PASSWORD in .env
- Verify user has permissions: `GRANT ALL ON DATABASE krile TO your_user;`

### "database does not exist"
- Create the database: `createdb krile` or use SQL: `CREATE DATABASE krile;`
