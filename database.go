package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type SeedQuestion struct {
	Text     string       `json:"text"`
	Category string       `json:"category"`
	Type     string       `json:"type"`
	ImageURL string       `json:"image_url"`
	Options  []SeedOption `json:"options"`
}

type SeedOption struct {
	Text  string `json:"text"`
	Color string `json:"color"`
}

func InitDB(filepath string) {
	var err error
	db, err = sql.Open("sqlite3", filepath)
	if err != nil {
		log.Fatal(err)
	}

	// 1. Enable WAL mode for reliability
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Fatal("Failed to enable WAL mode:", err)
	}

	// 2. Create Tables
	createTables()

	// 3. Sync Data from JSON
	seedData()
}

func createTables() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE,
			pin_hash TEXT,
			total_score INTEGER DEFAULT 0,
			room_code TEXT DEFAULT 'MAIN'
		);`,
		// Added 'correct_text_input' column
		`CREATE TABLE IF NOT EXISTS questions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			text TEXT,
			category TEXT,
			type TEXT DEFAULT 'select', 
			image_url TEXT,
			points INTEGER DEFAULT 1,
			status TEXT DEFAULT 'OPEN',
			correct_option_id INTEGER,
			correct_text_input TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS options (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			question_id INTEGER,
			text TEXT,
			color_hex TEXT,
			FOREIGN KEY(question_id) REFERENCES questions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS predictions (
			user_id INTEGER,
			question_id INTEGER,
			selected_option_id INTEGER,
			text_input TEXT,
			PRIMARY KEY (user_id, question_id),
			FOREIGN KEY(user_id) REFERENCES users(id),
			FOREIGN KEY(question_id) REFERENCES questions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			log.Fatalf("Error creating table: %s\nQuery: %s", err, query)
		}
	}

	// Migration: Add room_code column if not exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='room_code'").Scan(&count)
	if err == nil && count == 0 {
		log.Println("Migrating: Adding room_code to users table...")
		if _, err := db.Exec("ALTER TABLE users ADD COLUMN room_code TEXT DEFAULT 'MAIN'"); err != nil {
			log.Fatalf("Error migrating users table: %v", err)
		}
	}
}

func seedData() {
	// 1. Seed Game State
	var stateVal string
	err := db.QueryRow("SELECT value FROM settings WHERE key='game_status'").Scan(&stateVal)
	if err != nil {
		log.Println("Initializing Game State: OPEN")
		db.Exec("INSERT INTO settings (key, value) VALUES ('game_status', 'OPEN')")
	}

	// 2. Load Questions from JSON
	file, err := os.ReadFile("questions.json")
	if err != nil {
		log.Println("No questions.json found. Skipping seed.")
		return
	}

	var questions []SeedQuestion
	if err := json.Unmarshal(file, &questions); err != nil {
		log.Printf("Error parsing questions.json: %v", err)
		return
	}

	log.Println("Syncing questions from questions.json...")

	for _, q := range questions {
		var exists int
		err := db.QueryRow("SELECT COUNT(*) FROM questions WHERE text = ?", q.Text).Scan(&exists)
		if err == nil && exists == 0 {
			
			qType := q.Type
			if qType == "" { qType = "select" }

			res, err := db.Exec("INSERT INTO questions (text, category, type, image_url) VALUES (?, ?, ?, ?)", q.Text, q.Category, qType, q.ImageURL)
			if err != nil {
				log.Printf("Failed to insert question: %v", err)
				continue
			}
			qID, _ := res.LastInsertId()

			for _, o := range q.Options {
				db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", qID, o.Text, o.Color)
			}
		}
	}
}

// -- Helpers --
func getGameStatus() string {
	var status string
	err := db.QueryRow("SELECT value FROM settings WHERE key='game_status'").Scan(&status)
	if err != nil { return "OPEN" }
	return status
}

func setGameStatus(status string) {
	db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('game_status', ?)", status)
}