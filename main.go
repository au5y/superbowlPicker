package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// -- Data Structures --

type PageData struct {
	User        *User
	Categories  []CategoryGroup
	GameStatus  string
	TargetUser  *User
	RoomAliases map[string]string // NEW: Pass aliases to templates
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
	RoomCode   string `json:"room_code"`
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
	CorrectTextInput string
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

var AllowedRooms = map[string]string{
	"FAM":   "Miller/Young Family Pool",
	"BRAD":     "12 Bradbury Watchparty",
	"FRIENDS":  "Friends",
	"CFSV":     "Crossfit Somerville",
	"DRAPER":	"Draper",
	"GLOBAL":   "Just for Fun (Global)",
}

var funcMap = template.FuncMap{
	"split": func(s string, sep string) []string {
		if s == "" { return []string{} }
		return strings.Split(s, sep)
	},
	"roomName": func(code string, aliases map[string]string) string {
		if name, ok := aliases[code]; ok {
			return name
		}
		return code
	},
	"hasRoom": func(currentRooms string, roomToCheck string) bool {
		parts := strings.Split(currentRooms, ",")
		for _, p := range parts {
			if strings.TrimSpace(p) == roomToCheck {
				return true
			}
		}
		return false
	},
}

// Register funcs before parsing
var templates = template.Must(template.New("T").Funcs(funcMap).ParseGlob("templates/*.html"))

func main() {
	InitDB("./game.db")
	defer db.Close()

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/leaderboard", handleLeaderboardView)
	http.HandleFunc("/user/", handleUserProfile)
	http.HandleFunc("/user/update", handleUserUpdate)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/predict", handlePredict)
	http.HandleFunc("/api/leaderboard", handleLeaderboardAPI)
	http.HandleFunc("/admin", handleAdmin)
	http.HandleFunc("/admin/resolve", handleResolve)
	http.HandleFunc("/admin/state", handleGameState)
	http.HandleFunc("/admin/refresh", handleAdminRefresh)
	http.HandleFunc("/admin/users", handleAdminUsers)
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
	err = db.QueryRow("SELECT username, total_score, room_code FROM users WHERE id = ?", userID).Scan(&user.Username, &user.TotalScore, &user.RoomCode)
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

		optRows, _ := db.Query("SELECT id, text, color_hex FROM options WHERE question_id = ?", q.ID)
		for optRows.Next() {
			o := OptionData{}
			optRows.Scan(&o.ID, &o.Text, &o.ColorHex)
			q.Options = append(q.Options, o)
		}
		optRows.Close()

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

	// Pass RoomAliases to template
	render(w, "index.html", PageData{
		User: user, 
		Categories: grouped, 
		GameStatus: getGameStatus(),
		RoomAliases: AllowedRooms,
	})
}

func handleLeaderboardView(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil { http.Redirect(w, r, "/", http.StatusFound); return }
	render(w, "leaderboard.html", PageData{
		User: user, 
		GameStatus: getGameStatus(),
		RoomAliases: AllowedRooms,
	})
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

	// Use anonymous struct but include RoomAliases
	render(w, "profile.html", struct {
		User *User; TargetUser *User; Questions []QuestionData; GameStatus string; RoomAliases map[string]string
	}{currentUser, targetUser, questions, gameStatus, AllowedRooms})
}

func handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "Method Not Allowed", 405); return }

	user := getUser(r)
	if user == nil { http.Redirect(w, r, "/", 302); return }

	// 1. Update Username
	newUsername := strings.TrimSpace(r.FormValue("username"))
	if newUsername != "" {
		_, err := db.Exec("UPDATE users SET username = ? WHERE id = ?", newUsername, user.ID)
		if err != nil { log.Println("Error updating username:", err) }
	}

	// 2. Update Room Codes
	if r.Form.Has("room_codes") {
		rawRooms := r.FormValue("room_codes")
		var validatedRooms []string
		
		parts := strings.Split(rawRooms, ",")
		for _, p := range parts {
			clean := strings.ToUpper(strings.TrimSpace(p))
			// Validates against the map keys
			if _, ok := AllowedRooms[clean]; ok {
				validatedRooms = append(validatedRooms, clean)
			}
		}
		
		finalRoomCode := strings.Join(validatedRooms, ",")
		if finalRoomCode == "" { finalRoomCode = "GLOBAL" }

		_, err := db.Exec("UPDATE users SET room_code = ? WHERE id = ?", finalRoomCode, user.ID)
		if err != nil { log.Println("Error updating rooms:", err) }
	}

	ref := r.Header.Get("Referer")
	if ref == "" { ref = "/" }
	http.Redirect(w, r, ref, 302)
}

func handleLeaderboardAPI(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	scope := r.URL.Query().Get("scope")

	tieBreakerSQL := `COALESCE((
		SELECT SUM(CAST(p.text_input AS INTEGER)) 
		FROM predictions p 
		JOIN questions q ON p.question_id = q.id 
		WHERE p.user_id = u.id AND (q.type = 'scoreboard-left' OR q.type = 'scoreboard-right')
	), 0)`

	query := fmt.Sprintf(`SELECT u.id, u.username, u.total_score, %s as tie_breaker, u.room_code FROM users u`, tieBreakerSQL)

	var args []interface{}
	if scope != "global" && room != "" {
		query += " WHERE u.room_code LIKE ?"
		args = append(args, "%"+room+"%")
	}

	query += " ORDER BY u.total_score DESC, u.username ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Println(err)
		http.Error(w, "DB Error", 500)
		return
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		u := User{}
		rows.Scan(&u.ID, &u.Username, &u.TotalScore, &u.TieBreaker, &u.RoomCode)
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
	var pinHash string
	
	if err := db.QueryRow("SELECT id, pin_hash FROM users WHERE username = ? COLLATE NOCASE", username).Scan(&userID, &pinHash); err == nil {
		if pinHash != "" && pinHash != pin {
			http.Redirect(w, r, "/?error=invalid_pin", 302)
			return
		}
		if pinHash == "" {
			db.Exec("UPDATE users SET pin_hash = ? WHERE id = ?", pin, userID)
		}
	} else {
		// New user defaults to empty string room to trigger the gate
		res, _ := db.Exec("INSERT INTO users (username, pin_hash, room_code) VALUES (?, ?, ?)", username, pin, "")
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

	var users []User
	uRows, err := db.Query("SELECT id, username, room_code, total_score FROM users ORDER BY username ASC")
	if err == nil {
		defer uRows.Close()
		for uRows.Next() {
			u := User{}
			uRows.Scan(&u.ID, &u.Username, &u.RoomCode, &u.TotalScore)
			users = append(users, u)
		}
	}

	render(w, "admin.html", struct {
		Questions   []QuestionData
		GameStatus  string
		Users       []User
		RoomAliases map[string]string
	}{questions, getGameStatus(), users, AllowedRooms})
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuestionID string `json:"question_id"`
		OptionID   string `json:"option_id"`
		TextInput  string `json:"text_input"` 
		Status     string `json:"status"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Status == "OPEN" {
		db.Exec("UPDATE questions SET status='OPEN', correct_option_id=NULL, correct_text_input=NULL WHERE id=?", req.QuestionID)
	} else {
		var optID interface{} = nil
		if req.OptionID != "" { optID = req.OptionID }
		var txtVal interface{} = nil
		if req.TextInput != "" { txtVal = req.TextInput }
		db.Exec("UPDATE questions SET status='RESOLVED', correct_option_id=?, correct_text_input=? WHERE id=?", optID, txtVal, req.QuestionID)
	}
	
	db.Exec(`UPDATE users SET total_score = (SELECT COUNT(*) FROM predictions p JOIN questions q ON p.question_id = q.id WHERE p.user_id = users.id AND q.status = 'RESOLVED' AND p.selected_option_id = q.correct_option_id)`)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGameState(w http.ResponseWriter, r *http.Request) {
	status := r.FormValue("status")
	if status == "OPEN" || status == "LOCKED" { setGameStatus(status) }
	http.Redirect(w, r, "/admin?key=touchdown", 302)
}

func handleAdminRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "Method Not Allowed", 405); return }

	file, err := os.ReadFile("questions.json")
	if err != nil {
		log.Println("Error reading questions.json:", err)
		http.Redirect(w, r, "/admin?key=touchdown&error=read_failed", 302)
		return
	}

	var fileQuestions []SeedQuestion
	if err := json.Unmarshal(file, &fileQuestions); err != nil {
		log.Println("Error parsing questions.json:", err)
		http.Redirect(w, r, "/admin?key=touchdown&error=parse_failed", 302)
		return
	}

	dbQuestions := make(map[string]int)
	rows, _ := db.Query("SELECT id, text FROM questions")
	defer rows.Close()
	for rows.Next() {
		var id int
		var text string
		rows.Scan(&id, &text)
		dbQuestions[text] = id
	}

	seenTexts := make(map[string]bool)

	for _, q := range fileQuestions {
		seenTexts[q.Text] = true
		
		if id, exists := dbQuestions[q.Text]; exists {
			db.Exec("UPDATE questions SET category=?, type=?, image_url=? WHERE id=?", q.Category, q.Type, q.ImageURL, id)
		} else {
			qType := q.Type
			if qType == "" { qType = "select" }
			res, _ := db.Exec("INSERT INTO questions (text, category, type, image_url) VALUES (?, ?, ?, ?)", q.Text, q.Category, qType, q.ImageURL)
			newID, _ := res.LastInsertId()
			for _, o := range q.Options {
				db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", newID, o.Text, o.Color)
			}
		}
	}

	for text, id := range dbQuestions {
		if !seenTexts[text] {
			db.Exec("DELETE FROM predictions WHERE question_id = ?", id)
			db.Exec("DELETE FROM options WHERE question_id = ?", id)
			db.Exec("DELETE FROM questions WHERE id = ?", id)
		}
	}

	db.Exec(`UPDATE users SET total_score = (SELECT COUNT(*) FROM predictions p JOIN questions q ON p.question_id = q.id WHERE p.user_id = users.id AND q.status = 'RESOLVED' AND p.selected_option_id = q.correct_option_id)`)

	http.Redirect(w, r, "/admin?key=touchdown&status=refreshed", 302)
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	keys, ok := r.URL.Query()["key"]
	if !ok || len(keys[0]) < 1 || keys[0] != "touchdown" { http.Error(w, "Forbidden", 403); return }

	if r.Method != http.MethodPost { http.Error(w, "Method Not Allowed", 405); return }

	action := r.FormValue("action")
	userIDStr := r.FormValue("user_id")

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "Invalid User ID", 400)
		return
	}

	if action == "reset_pin" {
		db.Exec("UPDATE users SET pin_hash = '' WHERE id = ?", userID)
	} else if action == "delete" {
		db.Exec("DELETE FROM predictions WHERE user_id = ?", userID)
		db.Exec("DELETE FROM users WHERE id = ?", userID)
	} else if action == "update_room" {
		// r.FormValue only gets the first value, we need r.Form["room_code"]
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Form Error", 400)
			return
		}
		
		rawRooms := r.Form["room_code"] // Returns []string
		var validatedRooms []string
		
		for _, p := range rawRooms {
			clean := strings.ToUpper(strings.TrimSpace(p))
			if _, ok := AllowedRooms[clean]; ok {
				validatedRooms = append(validatedRooms, clean)
			}
		}
		
		finalRoom := strings.Join(validatedRooms, ",")
		if finalRoom == "" { finalRoom = "GLOBAL" } // Default to GLOBAL if they uncheck everything

		db.Exec("UPDATE users SET room_code = ? WHERE id = ?", finalRoom, userID)

	} else if action == "update_username" {
		newUsername := strings.TrimSpace(r.FormValue("new_username"))
		if newUsername != "" {
			db.Exec("UPDATE users SET username = ? WHERE id = ?", newUsername, userID)
		}
	}

	http.Redirect(w, r, "/admin?key=touchdown", 302)
}

func render(w http.ResponseWriter, tmpl string, data interface{}) {
	if err := templates.ExecuteTemplate(w, tmpl, data); err != nil { log.Println(err) }
}