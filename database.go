package main

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func InitDB(filepath string) {
	var err error
	db, err = sql.Open("sqlite3", filepath)
	if err != nil {
		log.Fatal(err)
	}

	// 1. Enable WAL mode for reliability (Recovery Strategy)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Fatal("Failed to enable WAL mode:", err)
	}

	// 2. Create Tables
	createTables()

	// 3. Seed Data if empty
	seedData()
}

func createTables() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE,
			pin_hash TEXT,
			total_score INTEGER DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS questions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			text TEXT,
			category TEXT,
			points INTEGER DEFAULT 1,
			status TEXT DEFAULT 'OPEN', -- OPEN, LOCKED, RESOLVED
			correct_option_id INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS options (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			question_id INTEGER,
			text TEXT,
			color_hex TEXT, -- Added for your Teal/Red requirement
			FOREIGN KEY(question_id) REFERENCES questions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS predictions (
			user_id INTEGER,
			question_id INTEGER,
			selected_option_id INTEGER,
			PRIMARY KEY (user_id, question_id),
			FOREIGN KEY(user_id) REFERENCES users(id),
			FOREIGN KEY(question_id) REFERENCES questions(id)
		);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			log.Fatalf("Error creating table: %s\nQuery: %s", err, query)
		}
	}
}

func seedData() {
	// Check if questions exist
	var count int
	row := db.QueryRow("SELECT COUNT(*) FROM questions")
	_ = row.Scan(&count)

	if count == 0 {
		log.Println("Seeding default data...")

		// Helper to insert question and options
		insertQ := func(text, cat string, opts ...map[string]string) {
			res, _ := db.Exec("INSERT INTO questions (text, category) VALUES (?, ?)", text, cat)
			qID, _ := res.LastInsertId()

			for _, o := range opts {
				db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", qID, o["text"], o["color"])
			}
		}

		// Seahawks (Teal: #002244 - using bright teal for visibility: #00D2BE) vs Patriots (Red: #C60C30)
		insertQ("Who will win the Super Bowl?", "Game",
			map[string]string{"text": "Seahawks", "color": "#00D2BE"},
			map[string]string{"text": "Patriots", "color": "#C60C30"},
		)
		insertQ("Who will win the Coin Toss?", "Pre-Game",
			map[string]string{"text": "Seahawks", "color": "#00D2BE"},
			map[string]string{"text": "Patriots", "color": "#C60C30"},
		)
		insertQ("Coin Toss Result?", "Pre-Game",
			map[string]string{"text": "Heads", "color": "#888888"},
			map[string]string{"text": "Tails", "color": "#888888"},
		)
	}
}