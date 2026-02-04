package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

// Generic Football Helmet Icon (Local Asset)
const HelmetIconURL = "/static/assets/helmet.svg"

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

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Fatal("Failed to enable WAL mode:", err)
	}

	createTables()
	runMigrations()
	seedData()
}

func createTables() {
	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE,
			pin_hash TEXT,
			total_score INTEGER DEFAULT 0,
			room_code TEXT DEFAULT 'MAIN',
			is_admin INTEGER DEFAULT 0,
			icon TEXT DEFAULT '%s',
			color_hex TEXT DEFAULT '#002244'
		);`, HelmetIconURL),
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
}

func runMigrations() {
	// 1. Legacy Room Code
	if !columnExists("users", "room_code") {
		db.Exec("ALTER TABLE users ADD COLUMN room_code TEXT DEFAULT 'MAIN'")
	}
	// 2. Admin
	if !columnExists("users", "is_admin") {
		db.Exec("ALTER TABLE users ADD COLUMN is_admin INTEGER DEFAULT 0")
	}
	// 3. Icon
	if !columnExists("users", "icon") {
		db.Exec(fmt.Sprintf("ALTER TABLE users ADD COLUMN icon TEXT DEFAULT '%s'", HelmetIconURL))
	}
	// 4. Color
	if !columnExists("users", "color_hex") {
		db.Exec("ALTER TABLE users ADD COLUMN color_hex TEXT DEFAULT '#002244'")
	}

	// 5. Fix old default '🏈' to Helmet URL if preferred
	db.Exec("UPDATE users SET icon = ? WHERE icon = '🏈'", HelmetIconURL)

	// 6. Fix old remote URL to local asset (Migration for v2.5_dev update)
	oldRemoteURL := "https://www.svgrepo.com/show/8996/american-football-helmet.svg"
	db.Exec("UPDATE users SET icon = ? WHERE icon = ?", HelmetIconURL, oldRemoteURL)

	// 7. Fix existing remote team URLs to local assets
	// This replaces the ESPN CDN prefix with the local static path for all users
	db.Exec("UPDATE users SET icon = REPLACE(icon, 'https://a.espncdn.com/i/teamlogos/nfl/500/', '/static/assets/') WHERE icon LIKE 'https://a.espncdn.com/i/teamlogos/nfl/500/%'")
}

func columnExists(tableName, columnName string) bool {
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name='%s'", tableName, columnName)
	err := db.QueryRow(query).Scan(&count)
	return err == nil && count > 0
}

func seedData() {
	var stateVal string
	err := db.QueryRow("SELECT value FROM settings WHERE key='game_status'").Scan(&stateVal)
	if err != nil {
		db.Exec("INSERT INTO settings (key, value) VALUES ('game_status', 'OPEN')")
	}

	file, err := os.ReadFile("questions.json")
	if err != nil {
		return
	}

	var questions []SeedQuestion
	if err := json.Unmarshal(file, &questions); err != nil {
		return
	}

	for _, q := range questions {
		var exists int
		err := db.QueryRow("SELECT COUNT(*) FROM questions WHERE text = ?", q.Text).Scan(&exists)
		if err == nil && exists == 0 {
			qType := q.Type
			if qType == "" {
				qType = "select"
			}
			res, _ := db.Exec("INSERT INTO questions (text, category, type, image_url) VALUES (?, ?, ?, ?)", q.Text, q.Category, qType, q.ImageURL)
			qID, _ := res.LastInsertId()
			for _, o := range q.Options {
				db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", qID, o.Text, o.Color)
			}
		}
	}
}

func getGameStatus() string {
	var status string
	err := db.QueryRow("SELECT value FROM settings WHERE key='game_status'").Scan(&status)
	if err != nil {
		return "OPEN"
	}
	return status
}

func setGameStatus(status string) {
	db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('game_status', ?)", status)
}

func SetAdminStatus(username string, isAdmin bool) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count); err != nil || count == 0 {
		return fmt.Errorf("user not found")
	}
	val := 0
	if isAdmin {
		val = 1
	}
	_, err := db.Exec("UPDATE users SET is_admin = ? WHERE username = ?", val, username)
	return err
}
