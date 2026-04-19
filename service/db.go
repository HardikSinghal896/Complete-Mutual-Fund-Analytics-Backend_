package service

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var db *sql.DB

// InitDB opens and validates a MySQL connection.
// DSN is read from the MF_DSN environment variable, falling back to a
// local default so the binary works out-of-the-box in development.
//
//	export MF_DSN="user:pass@tcp(127.0.0.1:3306)/mfmvp?parseTime=true"
func InitDB() (*sql.DB, error) {
	dsn := os.Getenv("MF_DSN")
	if dsn == "" {
	    dsn = "mfuser:mfpass@tcp(127.0.0.1:3306)/mfmvp?parseTime=true"
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	// Sane pool settings for a lightweight background service.
	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}

	log.Println("[db] connected to MySQL")
	db = conn
	return conn, nil
}

// GetDB returns the package-level DB handle initialised by InitDB.
func GetDB() *sql.DB {
	return db
}
