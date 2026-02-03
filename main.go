package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
)

// -- Data Structures for Templates --

type PageData struct {
	User      *User
	Questions []QuestionData
}

type User struct {
	ID         int
	Username   string
	TotalScore int
}

type QuestionData struct {
	ID       int
	Text     string
	Category string
	Options  []OptionData
}

type OptionData struct {
	ID         int
	Text       string
	ColorHex   string
	IsSelected bool // Helper for the UI to show "checked" state
}

type PredictionRequest struct {
	QuestionID int `json:"question_id"`
	OptionID   int `json:"option_id"`
}

// Global templates cache
var templates = template.Must(template.ParseGlob("templates/*.html"))

func main() {
	// 1. Initialize Database
	InitDB("./game.db")
	defer db.Close()

	// 2. Setup Routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/predict", handlePredict)
	
	// 3. Serve Static Files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 4. Start Server
	port := ":4884"
	fmt.Printf("🏈 Superbowl Picker running at http://localhost%s\n", port)
	err := http.ListenAndServe(port, nil)
	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

// -- Handlers --

func handleIndex(w http.ResponseWriter, r *http.Request) {
	// 1. Check for Session Cookie
	cookie, err := r.Cookie("user_id")
	
	// If no user, render the Login View (PageData with User=nil)
	if err != nil || cookie.Value == "" {
		render(w, "index.html", PageData{User: nil})
		return
	}

	userID, _ := strconv.Atoi(cookie.Value)

	// 2. Fetch User Details
	user := &User{ID: userID}
	err = db.QueryRow("SELECT username, total_score FROM users WHERE id = ?", userID).Scan(&user.Username, &user.TotalScore)
	if err != nil {
		// Cookie is invalid (user deleted?), force logout
		http.Redirect(w, r, "/logout", http.StatusFound)
		return
	}

	// 3. Fetch Questions & Options
	// We fetch all questions, options, AND the user's current prediction in one go would be complex SQL.
	// Simpler approach: Fetch Questions -> Fetch Options -> Mark Selected.
	
	questions := []QuestionData{}
	
	// Get all OPEN questions (or all questions if you want to see history)
	rows, _ := db.Query("SELECT id, text, category FROM questions ORDER BY id ASC")
	defer rows.Close()

	for rows.Next() {
		q := QuestionData{}
		rows.Scan(&q.ID, &q.Text, &q.Category)
		
		// Get Options for this question
		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		optRows.Close()

		// Check what the user predicted
		var selectedOptionID int
		_ = db.QueryRow("SELECT selected_option_id FROM predictions WHERE user_id = ? AND question_id = ?", userID, q.ID).Scan(&selectedOptionID)

		// Mark the option as selected in the struct
		for i := range q.Options {
			if q.Options[i].ID == selectedOptionID {
				q.Options[i].IsSelected = true
			}
		}

		questions = append(questions, q)
	}

	// 4. Render Dashboard
	data := PageData{
		User:      user,
		Questions: questions,
	}
	render(w, "index.html", data)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	username := r.FormValue("username")
	pin := r.FormValue("pin") // In a real app, hash this!

	var userID int

	// Check if user exists
	err := db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&userID)
	
	if err == nil {
		// User exists - simple logic: we aren't checking PIN strictly for this demo, 
		// but you'd check `WHERE username=? AND pin_hash=?` here.
		// For now, we just log them in.
	} else {
		// Create new user
		res, err := db.Exec("INSERT INTO users (username, pin_hash) VALUES (?, ?)", username, pin)
		if err != nil {
			http.Error(w, "Username taken or invalid", http.StatusBadRequest)
			return
		}
		id, _ := res.LastInsertId()
		userID = int(id)
	}

	// Set Cookie (Simple insecure cookie for local party)
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

func handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Get User
	cookie, err := r.Cookie("user_id")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, _ := strconv.Atoi(cookie.Value)

	// 2. Parse JSON Payload
	var req PredictionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 3. Save to DB (UPSERT)
	// SQLite `INSERT OR REPLACE` or `ON CONFLICT` logic
	query := `
		INSERT INTO predictions (user_id, question_id, selected_option_id)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, question_id) 
		DO UPDATE SET selected_option_id = excluded.selected_option_id;
	`
	_, err = db.Exec(query, userID, req.QuestionID, req.OptionID)
	if err != nil {
		log.Println("Error saving prediction:", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Helper to render templates
func render(w http.ResponseWriter, tmpl string, data interface{}) {
	err := templates.ExecuteTemplate(w, tmpl, data)
	if err != nil {
		log.Println("Template Error:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}