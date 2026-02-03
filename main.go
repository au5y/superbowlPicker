package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// -- Data Structures --

type PageData struct {
	User       *User
	Categories []CategoryGroup
	GameStatus string
	TargetUser *User
}

type CategoryGroup struct {
	Name      string
	Anchor    string
	Questions []QuestionData
}

type User struct {
	ID         int    `json:"id"`
	Username   string `json:"username"`
	TotalScore int    `json:"total_score"`
	TieBreaker string `json:"tie_breaker"`
}

type QuestionData struct {
	ID               int
	Text             string
	Category         string
	Status           string
	Type             string
	ImageURL         string
	Options          []OptionData
	CorrectOptionID  int64
	CorrectTextInput string // NEW field
	UserPrediction   *PredictionData
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

var templates = template.Must(template.ParseGlob("templates/*.html"))

func main() {
	InitDB("./game.db")
	defer db.Close()

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/leaderboard", handleLeaderboardView)
	http.HandleFunc("/user/", handleUserProfile)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/predict", handlePredict)
	http.HandleFunc("/api/leaderboard", handleLeaderboardAPI)
	http.HandleFunc("/admin", handleAdmin)
	http.HandleFunc("/admin/resolve", handleResolve)
	http.HandleFunc("/admin/state", handleGameState)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	port := ":4884"
	fmt.Printf("🏈 Superbowl LX Prop Pool running at http://localhost%s\n", port)
	err := http.ListenAndServe(port, nil)
	if err != nil {
		log.Fatal(err)
	}
}

// -- Helpers --
func getUser(r *http.Request) *User {
	cookie, err := r.Cookie("user_id")
	if err != nil || cookie.Value == "" { return nil }
	userID, _ := strconv.Atoi(cookie.Value)
	user := &User{ID: userID}
	err = db.QueryRow("SELECT username, total_score FROM users WHERE id = ?", userID).Scan(&user.Username, &user.TotalScore)
	if err != nil { return nil }
	return user
}

// -- Handlers --

func handleIndex(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil {
		render(w, "index.html", PageData{User: nil})
		return
	}

	// Fetch ALL Questions
	var questions []QuestionData
	// Note: We don't fetch CorrectTextInput here usually, but we can to be consistent
	rows, err := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id FROM questions ORDER BY id ASC")
	if err != nil { http.Error(w, "DB Error", 500); return }
	defer rows.Close()

	for rows.Next() {
		q := QuestionData{}
		var correctOptID sql.NullInt64
		var imgURL sql.NullString
		
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &correctOptID)
		
		if correctOptID.Valid { q.CorrectOptionID = correctOptID.Int64 }
		if imgURL.Valid { q.ImageURL = imgURL.String }

		// Fetch Options
		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		optRows.Close()

		// Fetch Predictions
		var selID sql.NullInt64
		var txtInput sql.NullString
		err := db.QueryRow("SELECT selected_option_id, text_input FROM predictions WHERE user_id = ? AND question_id = ?", user.ID, q.ID).Scan(&selID, &txtInput)
		if err == nil {
			if q.Type == "select" && selID.Valid {
				for i := range q.Options {
					if int64(q.Options[i].ID) == selID.Int64 {
						q.Options[i].IsSelected = true
					}
				}
			}
			if q.Type != "select" && txtInput.Valid {
				q.UserPrediction = &PredictionData{TextInput: txtInput.String}
			}
		}
		questions = append(questions, q)
	}

	// Grouping Logic
	var grouped []CategoryGroup
	groupMap := make(map[string]int)

	for _, q := range questions {
		idx, exists := groupMap[q.Category]
		if !exists {
			slug := strings.ReplaceAll(strings.ToLower(q.Category), " ", "-")
			newGroup := CategoryGroup{Name: q.Category, Anchor: slug, Questions: []QuestionData{q}}
			grouped = append(grouped, newGroup)
			groupMap[q.Category] = len(grouped) - 1
		} else {
			grouped[idx].Questions = append(grouped[idx].Questions, q)
		}
	}

	render(w, "index.html", PageData{User: user, Categories: grouped, GameStatus: getGameStatus()})
}

func handleLeaderboardView(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil { http.Redirect(w, r, "/", http.StatusFound); return }
	render(w, "leaderboard.html", PageData{User: user, GameStatus: getGameStatus()})
}

func handleUserProfile(w http.ResponseWriter, r *http.Request) {
	currentUser := getUser(r)
	if currentUser == nil { http.Redirect(w, r, "/", http.StatusFound); return }
	pathID := r.URL.Path[len("/user/"):]
	targetID, err := strconv.Atoi(pathID)
	if err != nil { http.Error(w, "Invalid ID", 400); return }

	gameStatus := getGameStatus()
	if gameStatus == "OPEN" && currentUser.ID != targetID {
		render(w, "profile.html", PageData{User: currentUser, GameStatus: gameStatus, TargetUser: nil})
		return
	}

	targetUser := &User{ID: targetID}
	db.QueryRow("SELECT username, total_score FROM users WHERE id = ?", targetID).Scan(&targetUser.Username, &targetUser.TotalScore)

	questions := []QuestionData{}
	// Updated Query to fetch CorrectTextInput for the profile view
	rows, _ := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id, correct_text_input FROM questions ORDER BY id ASC")
	defer rows.Close()
	for rows.Next() {
		q := QuestionData{}
		var cID sql.NullInt64
		var imgURL sql.NullString
		var correctText sql.NullString
		
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &cID, &correctText)
		
		if cID.Valid { q.CorrectOptionID = cID.Int64 }
		if imgURL.Valid { q.ImageURL = imgURL.String }
		if correctText.Valid { q.CorrectTextInput = correctText.String }
		
		oRows, _ := db.Query("SELECT id, text FROM options WHERE question_id=?", q.ID)
		for oRows.Next() {
			o := OptionData{}
			oRows.Scan(&o.ID, &o.Text)
			q.Options = append(q.Options, o)
		}
		
		var sID sql.NullInt64
		var txt sql.NullString
		db.QueryRow("SELECT selected_option_id, text_input FROM predictions WHERE user_id=? AND question_id=?", targetID, q.ID).Scan(&sID, &txt)
		
		if sID.Valid || txt.Valid {
			pred := &PredictionData{}
			if sID.Valid { pred.SelectedOptionID = sID.Int64 }
			if txt.Valid { pred.TextInput = txt.String }
			if q.Status == "RESOLVED" && q.Type == "select" {
				pred.IsCorrect = (pred.SelectedOptionID == q.CorrectOptionID)
			}
			q.UserPrediction = pred
		}
		questions = append(questions, q)
	}

	render(w, "profile.html", struct {
		User *User; TargetUser *User; Questions []QuestionData; GameStatus string
	}{currentUser, targetUser, questions, gameStatus})
}

func handleLeaderboardAPI(w http.ResponseWriter, r *http.Request) {
	query := `SELECT u.id, u.username, u.total_score, COALESCE((SELECT p.text_input FROM predictions p JOIN questions q ON p.question_id = q.id WHERE p.user_id = u.id AND q.type = 'number' AND q.text LIKE '%Total Points%' LIMIT 1), '-') as tie_breaker FROM users u ORDER BY u.total_score DESC, u.username ASC`
	rows, _ := db.Query(query)
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
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	if getGameStatus() == "LOCKED" { http.Error(w, "Locked", 403); return }
	user := getUser(r)
	if user == nil { http.Error(w, "Auth", 401); return }
	
	var req PredictionRequest
	json.NewDecoder(r.Body).Decode(&req)
	
	var optID interface{} = nil
	if req.OptionID != "" { optID = req.OptionID }
	var txtInput interface{} = nil
	if req.TextInput != "" { txtInput = req.TextInput }

	_, err := db.Exec(`INSERT INTO predictions (user_id, question_id, selected_option_id, text_input) VALUES (?, ?, ?, ?) ON CONFLICT(user_id, question_id) DO UPDATE SET selected_option_id=excluded.selected_option_id, text_input=excluded.text_input`, user.ID, req.QuestionID, optID, txtInput)
	if err != nil { log.Println(err); http.Error(w, "DB", 500); return }
	w.WriteHeader(200)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Redirect(w, r, "/", 302); return }
	username := r.FormValue("username")
	pin := r.FormValue("pin")
	var userID int
	if err := db.QueryRow("SELECT id FROM users WHERE username=?", username).Scan(&userID); err != nil {
		res, _ := db.Exec("INSERT INTO users (username, pin_hash) VALUES (?, ?)", username, pin)
		id, _ := res.LastInsertId()
		userID = int(id)
	}
	http.SetCookie(w, &http.Cookie{Name: "user_id", Value: strconv.Itoa(userID), Expires: time.Now().Add(24*time.Hour)})
	http.Redirect(w, r, "/", 302)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "user_id", MaxAge: -1})
	http.Redirect(w, r, "/", 302)
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	keys, ok := r.URL.Query()["key"]
	if !ok || len(keys[0]) < 1 || keys[0] != "touchdown" { http.Error(w, "Forbidden", 403); return }
	
	var questions []QuestionData
	// Updated Query: Fetch correct_text_input
	rows, _ := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id, correct_text_input FROM questions ORDER BY id ASC")
	defer rows.Close()
	for rows.Next() {
		q := QuestionData{}
		var c sql.NullInt64
		var imgURL sql.NullString
		var correctText sql.NullString
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &c, &correctText)
		
		if c.Valid { q.CorrectOptionID = c.Int64 }
		if imgURL.Valid { q.ImageURL = imgURL.String }
		if correctText.Valid { q.CorrectTextInput = correctText.String }

		oRows, _ := db.Query("SELECT id, text FROM options WHERE question_id=?", q.ID)
		for oRows.Next() {
			o := OptionData{}
			oRows.Scan(&o.ID, &o.Text)
			q.Options = append(q.Options, o)
		}
		questions = append(questions, q)
	}
	render(w, "admin.html", struct{Questions []QuestionData; GameStatus string}{questions, getGameStatus()})
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuestionID string `json:"question_id"`
		OptionID   string `json:"option_id"`
		TextInput  string `json:"text_input"` // New JSON field
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Prepare nullable args
	var optID interface{} = nil
	if req.OptionID != "" { optID = req.OptionID }
	
	var txtVal interface{} = nil
	if req.TextInput != "" { txtVal = req.TextInput }

	// Update both option_id AND text_input (one will usually be null)
	db.Exec("UPDATE questions SET status='RESOLVED', correct_option_id=?, correct_text_input=? WHERE id=?", optID, txtVal, req.QuestionID)
	
	// Recalculate scores (Only counts matching OptionIDs, ignores text inputs for score)
	db.Exec(`UPDATE users SET total_score = (SELECT COUNT(*) FROM predictions p JOIN questions q ON p.question_id = q.id WHERE p.user_id = users.id AND q.status = 'RESOLVED' AND p.selected_option_id = q.correct_option_id)`)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGameState(w http.ResponseWriter, r *http.Request) {
	status := r.FormValue("status")
	if status == "OPEN" || status == "LOCKED" { setGameStatus(status) }
	http.Redirect(w, r, "/admin?key=touchdown", 302)
}

func render(w http.ResponseWriter, tmpl string, data interface{}) {
	if err := templates.ExecuteTemplate(w, tmpl, data); err != nil { log.Println(err) }
}