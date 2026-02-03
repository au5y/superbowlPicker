package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
)

// -- Data Structures --

type PageData struct {
	User       *User
	Questions  []QuestionData
	GameStatus string
	TargetUser *User // For Profile View
}

type User struct {
	ID         int    `json:"id"`
	Username   string `json:"username"`
	TotalScore int    `json:"total_score"`
	TieBreaker string `json:"tie_breaker"` // Populated for leaderboard
}

type QuestionData struct {
	ID              int
	Text            string
	Category        string
	Status          string
	Type            string // "select" or "number"
	Options         []OptionData
	CorrectOptionID int64 // For checking correctness

	// For Profile View / Dashboard
	UserPrediction *PredictionData
}

type OptionData struct {
	ID         int
	Text       string
	ColorHex   string
	IsSelected bool
}

type PredictionData struct {
	SelectedOptionID int64
	TextInput        string
	IsCorrect        bool
}

type PredictionRequest struct {
	QuestionID string `json:"question_id"`
	OptionID   string `json:"option_id"`
	TextInput  string `json:"text_input"`
}

// Global templates cache
var templates = template.Must(template.ParseGlob("templates/*.html"))

func main() {
	// 1. Initialize Database
	InitDB("./game.db")
	defer db.Close()

	// 2. Setup Routes
	// Views
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/leaderboard", handleLeaderboardView)
	http.HandleFunc("/user/", handleUserProfile)
	
	// Auth
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)

	// API / Actions
	http.HandleFunc("/predict", handlePredict)
	http.HandleFunc("/api/leaderboard", handleLeaderboardAPI)

	// Admin
	http.HandleFunc("/admin", handleAdmin)
	http.HandleFunc("/admin/resolve", handleResolve)
	http.HandleFunc("/admin/state", handleGameState)

	// Static Files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 3. Start Server
	port := ":4884"
	fmt.Printf("🏈 Superbowl LX Prop Pool running at http://localhost%s\n", port)
	err := http.ListenAndServe(port, nil)
	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

// -- Middleware / Helpers --

func getUser(r *http.Request) *User {
	cookie, err := r.Cookie("user_id")
	if err != nil || cookie.Value == "" {
		return nil
	}

	userID, _ := strconv.Atoi(cookie.Value)
	user := &User{ID: userID}
	err = db.QueryRow("SELECT username, total_score FROM users WHERE id = ?", userID).Scan(&user.Username, &user.TotalScore)
	if err != nil {
		return nil
	}
	return user
}

// -- Handlers --

func handleIndex(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil {
		render(w, "index.html", PageData{User: nil})
		return
	}

	// Fetch Questions
	questions := []QuestionData{}
	rows, err := db.Query("SELECT id, text, category, status, type FROM questions ORDER BY id ASC")
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	defer rows.Close()

	for rows.Next() {
		q := QuestionData{}
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type)

		// Fetch Options
		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		optRows.Close()

		// Fetch User Prediction
		var selID sql.NullInt64
		var txtInput sql.NullString
		err := db.QueryRow("SELECT selected_option_id, text_input FROM predictions WHERE user_id = ? AND question_id = ?", user.ID, q.ID).Scan(&selID, &txtInput)

		if err == nil {
			// Mark options as selected for UI
			if q.Type == "select" && selID.Valid {
				for i := range q.Options {
					if int64(q.Options[i].ID) == selID.Int64 {
						q.Options[i].IsSelected = true
					}
				}
			}
			// Attach text input for number fields
			if q.Type == "number" && txtInput.Valid {
				q.UserPrediction = &PredictionData{TextInput: txtInput.String}
			}
		}

		questions = append(questions, q)
	}

	render(w, "index.html", PageData{User: user, Questions: questions, GameStatus: getGameStatus()})
}

func handleLeaderboardView(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	render(w, "leaderboard.html", PageData{User: user, GameStatus: getGameStatus()})
}

func handleUserProfile(w http.ResponseWriter, r *http.Request) {
	currentUser := getUser(r)
	if currentUser == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Extract target user ID from URL /user/{id}
	pathID := r.URL.Path[len("/user/"):]
	targetID, err := strconv.Atoi(pathID)
	if err != nil {
		http.Error(w, "Invalid User ID", 400)
		return
	}

	// Privacy Check: Only show other profiles if Game is LOCKED (or if looking at self)
	gameStatus := getGameStatus()
	if gameStatus == "OPEN" && currentUser.ID != targetID {
		render(w, "profile.html", PageData{
			User:       currentUser,
			GameStatus: gameStatus,
			TargetUser: nil, // This triggers the "Hidden" view in template
		})
		return
	}

	// Fetch Target User details
	targetUser := &User{ID: targetID}
	err = db.QueryRow("SELECT username, total_score FROM users WHERE id = ?", targetID).Scan(&targetUser.Username, &targetUser.TotalScore)
	if err != nil {
		http.Error(w, "User not found", 404)
		return
	}

	// Fetch Questions + Their Predictions
	questions := []QuestionData{}
	rows, err := db.Query("SELECT id, text, category, status, type, correct_option_id FROM questions ORDER BY id ASC")
	if err != nil {
		http.Error(w, "DB Error", 500)
		return
	}
	defer rows.Close()

	for rows.Next() {
		q := QuestionData{}
		var correctOptID sql.NullInt64
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &correctOptID)
		if correctOptID.Valid {
			q.CorrectOptionID = correctOptID.Int64
		}

		// Fetch Options (needed to show names of picks)
		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		optRows.Close()

		// Fetch Prediction
		var selID sql.NullInt64
		var txtInput sql.NullString
		err := db.QueryRow("SELECT selected_option_id, text_input FROM predictions WHERE user_id = ? AND question_id = ?", targetID, q.ID).Scan(&selID, &txtInput)

		if err == nil {
			pred := &PredictionData{}
			if selID.Valid {
				pred.SelectedOptionID = selID.Int64
			}
			if txtInput.Valid {
				pred.TextInput = txtInput.String
			}

			// Determine Correctness
			// Only grade "Select" types automatically. Number types are manual/tie-breakers.
			if q.Status == "RESOLVED" && q.Type == "select" {
				pred.IsCorrect = (pred.SelectedOptionID == q.CorrectOptionID)
			}
			q.UserPrediction = pred
		}

		questions = append(questions, q)
	}

	render(w, "profile.html", PageData{
		User:       currentUser,
		TargetUser: targetUser,
		Questions:  questions,
		GameStatus: gameStatus,
	})
}

// -- API Handlers --

func handleLeaderboardAPI(w http.ResponseWriter, r *http.Request) {
	// Find the Tie Breaker question ID for "Total Points"
	// We use a subquery to pull the specific text input for that question
	query := `
		SELECT 
			u.id, 
			u.username, 
			u.total_score,
			COALESCE((
				SELECT p.text_input 
				FROM predictions p 
				JOIN questions q ON p.question_id = q.id 
				WHERE p.user_id = u.id 
				AND q.type = 'number' 
				AND q.text LIKE '%Total Points%'
				LIMIT 1
			), '-') as tie_breaker
		FROM users u
		ORDER BY u.total_score DESC, u.username ASC
	`
	rows, err := db.Query(query)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u := User{}
		rows.Scan(&u.ID, &u.Username, &u.TotalScore, &u.TieBreaker)
		users = append(users, u)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	if getGameStatus() == "LOCKED" {
		http.Error(w, "Game is Locked", 403)
		return
	}

	user := getUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}

	var req PredictionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad JSON", 400)
		return
	}

	// Handle Nullables for SQL
	var optID interface{}
	var txtInput interface{}

	if req.OptionID != "" {
		optID = req.OptionID
	} else {
		optID = nil
	}
	if req.TextInput != "" {
		txtInput = req.TextInput
	} else {
		txtInput = nil
	}

	// UPSERT Logic
	query := `
		INSERT INTO predictions (user_id, question_id, selected_option_id, text_input)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, question_id) 
		DO UPDATE SET selected_option_id = excluded.selected_option_id, text_input = excluded.text_input;
	`
	_, err := db.Exec(query, user.ID, req.QuestionID, optID, txtInput)
	if err != nil {
		log.Println("DB Error:", err)
		http.Error(w, "DB Error", 500)
		return
	}
	w.WriteHeader(200)
}

// -- Auth Handlers --

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	username := r.FormValue("username")
	pin := r.FormValue("pin") // In production, hash this PIN

	var userID int
	// Check if user exists
	err := db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&userID)

	if err != nil {
		// User does not exist, create new one
		res, err := db.Exec("INSERT INTO users (username, pin_hash) VALUES (?, ?)", username, pin)
		if err != nil {
			http.Error(w, "Username taken or invalid", http.StatusBadRequest)
			return
		}
		id, _ := res.LastInsertId()
		userID = int(id)
	} else {
		// User exists.
		// NOTE: For this simple party app, we aren't enforcing the PIN check on login
		// to allow easy re-entry. If you want strict security, check pin_hash here.
	}

	// Set Cookie
	http.SetCookie(w, &http.Cookie{
		Name:    "user_id",
		Value:   strconv.Itoa(userID),
		Expires: time.Now().Add(24 * time.Hour),
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "user_id",
		Value:  "",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// -- Admin Handlers --

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	// Simple Security Check
	keys, ok := r.URL.Query()["key"]
	if !ok || len(keys[0]) < 1 || keys[0] != "touchdown" {
		http.Error(w, "Forbidden: Missing admin key (?key=touchdown)", http.StatusForbidden)
		return
	}

	questions := []QuestionData{}
	rows, err := db.Query("SELECT id, text, category, status, type, correct_option_id FROM questions ORDER BY id ASC")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	for rows.Next() {
		q := QuestionData{}
		var corr sql.NullInt64
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &corr)
		if corr.Valid {
			q.CorrectOptionID = corr.Int64
		}

		// Fetch options so admin can select winner
		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		questions = append(questions, q)
	}
	render(w, "admin.html", PageData{Questions: questions, GameStatus: getGameStatus()})
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		QuestionID string `json:"question_id"`
		OptionID   string `json:"option_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad JSON", 400)
		return
	}

	if req.QuestionID == "" || req.OptionID == "" {
		http.Error(w, "Missing ID", 400)
		return
	}

	// 1. Update the Question to RESOLVED and set the Winner
	_, err := db.Exec("UPDATE questions SET status='RESOLVED', correct_option_id=? WHERE id=?", req.OptionID, req.QuestionID)
	if err != nil {
		log.Println("Error resolving question:", err)
		http.Error(w, "Database error", 500)
		return
	}

	// 2. Recalculate ALL Scores
	// This query counts all correct predictions for 'select' type questions that are resolved
	recalcQuery := `
		UPDATE users 
		SET total_score = (
			SELECT COUNT(*) 
			FROM predictions p 
			JOIN questions q ON p.question_id = q.id 
			WHERE p.user_id = users.id 
			AND q.status = 'RESOLVED' 
			AND p.selected_option_id = q.correct_option_id
		);
	`
	_, err = db.Exec(recalcQuery)
	if err != nil {
		log.Println("Score recalc error:", err)
	}

	// 3. Return Success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGameState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	status := r.FormValue("status")
	if status == "OPEN" || status == "LOCKED" {
		setGameStatus(status)
	}

	// Redirect back to admin panel
	http.Redirect(w, r, "/admin?key=touchdown", http.StatusFound)
}

// -- Helpers --

func render(w http.ResponseWriter, tmpl string, data interface{}) {
	err := templates.ExecuteTemplate(w, tmpl, data)
	if err != nil {
		log.Println("Template Error:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}