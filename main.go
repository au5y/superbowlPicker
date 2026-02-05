package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PageData struct {
	User        *User
	Categories  []CategoryGroup
	GameStatus  string
	TargetUser  *User
	RoomAliases map[string]string
	ActivePage  string
	Questions   []QuestionData
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
	IsAdmin    bool   `json:"is_admin"`
	Icon       string `json:"icon"`
	ColorHex   string `json:"color_hex"`
}

type LeaderboardEntry struct {
	ID             int            `json:"id"`
	Username       string         `json:"username"`
	TotalScore     int            `json:"total_score"`
	TieBreaker     string         `json:"tie_breaker"`
	TBLeft         int            `json:"tb_left"`
	TBRight        int            `json:"tb_right"`
	RoomCode       string         `json:"room_code"`
	Icon           string         `json:"icon"`
	ColorHex       string         `json:"color_hex"`
	CategoryScores map[string]int `json:"category_scores"`
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
	"FAM":     "Miller/Young Family Pool",
	"BRAD":    "12 Bradbury Watchparty",
	"FRIENDS": "Friends",
	"CFSV":    "Crossfit Somerville",
	"DRAPER":  "Draper",
}

var funcMap = template.FuncMap{
	"split": func(s string, sep string) []string {
		if s == "" {
			return []string{}
		}
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

var templates = template.Must(template.New("T").Funcs(funcMap).ParseGlob("templates/*.html"))
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func setupLogging() {
	if _, err := os.Stat("logs"); os.IsNotExist(err) {
		os.Mkdir("logs", 0755)
	}
	logName := fmt.Sprintf("logs/server_%s.log", time.Now().Format("2006-01-02"))
	file, err := os.OpenFile(logName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Println("Failed to open log file, using stderr only")
		return
	}
	mw := io.MultiWriter(os.Stdout, file)
	log.SetOutput(mw)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func withLogging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(ww, r)
		duration := time.Since(start)
		clientIP := r.Header.Get("X-Forwarded-For")
		if clientIP == "" {
			clientIP = r.RemoteAddr
		}
		log.Printf("| %3d | %10v | %s | %s %s", ww.status, duration, clientIP, r.Method, r.URL.Path)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func main() {
	setupLogging()
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "./game.db"
	}
	InitDB(dbName)
	defer db.Close()

	if len(os.Args) > 1 {
		cmd := os.Args[1]
		if cmd == "admin" || cmd == "deadmin" {
			if len(os.Args) < 3 {
				fmt.Println("Usage: go run . [admin|deadmin] <username>")
				os.Exit(1)
			}
			username := os.Args[2]
			isAdmin := (cmd == "admin")
			if err := SetAdminStatus(username, isAdmin); err != nil {
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("User '%s' updated. Admin: %v\n", username, isAdmin)
			os.Exit(0)
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "4884"
	}
	fmt.Printf("Starting App on Port %s using DB %s\n", port, dbName)

	http.HandleFunc("/", withLogging(handleIndex))
	http.HandleFunc("/rules", withLogging(handleRules))
	http.HandleFunc("/leaderboard", withLogging(handleLeaderboardView))
	http.HandleFunc("/user/", withLogging(handleUserProfile))
	http.HandleFunc("/user/update", withLogging(handleUserUpdate))
	http.HandleFunc("/login", withLogging(handleLogin))
	http.HandleFunc("/logout", withLogging(handleLogout))
	http.HandleFunc("/predict", withLogging(handlePredict))
	http.HandleFunc("/api/leaderboard", withLogging(handleLeaderboardAPI))
	http.HandleFunc("/api/status", withLogging(handleGameStatusAPI))
	http.HandleFunc("/admin", withLogging(requireAdmin(handleAdmin)))
	http.HandleFunc("/admin/resolve", withLogging(requireAdmin(handleResolve)))
	http.HandleFunc("/admin/state", withLogging(requireAdmin(handleGameState)))
	http.HandleFunc("/admin/refresh", withLogging(requireAdmin(handleAdminRefresh)))
	http.HandleFunc("/admin/users", withLogging(requireAdmin(handleAdminUsers)))
	http.HandleFunc("/admin/backup", withLogging(requireAdmin(handleAdminBackup)))
	http.HandleFunc("/results", withLogging(handleResults))
	http.HandleFunc("/admin/question/edit", withLogging(requireAdmin(handleAdminEditQuestion)))
	http.HandleFunc("/admin/question/delete", withLogging(requireAdmin(handleAdminDeleteQuestion)))
	http.HandleFunc("/admin/option/edit", withLogging(requireAdmin(handleAdminEditOption)))
	http.HandleFunc("/admin/option/delete", withLogging(requireAdmin(handleAdminDeleteOption)))
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal(err)
	}
}

func handleRules(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	render(w, "rules.html", PageData{User: user, ActivePage: "rules"})
}

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getUser(r)
		if user == nil || !user.IsAdmin {
			http.Error(w, "403 Forbidden: Admin Access Required", 403)
			return
		}
		next(w, r)
	}
}

func getUser(r *http.Request) *User {
	cookie, err := r.Cookie("user_id")
	if err != nil || cookie.Value == "" {
		return nil
	}
	userID, _ := strconv.Atoi(cookie.Value)
	user := &User{ID: userID}
	var isAdminInt int
	err = db.QueryRow("SELECT username, total_score, room_code, is_admin, icon, color_hex FROM users WHERE id = ?", userID).Scan(&user.Username, &user.TotalScore, &user.RoomCode, &isAdminInt, &user.Icon, &user.ColorHex)
	if err != nil {
		return nil
	}
	user.IsAdmin = (isAdminInt == 1)
	return user
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	questionsMap := make(map[int]*QuestionData)
	var questionOrder []*QuestionData
	rows, err := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id FROM questions ORDER BY id ASC")
	if err != nil {
		http.Error(w, "DB Error", 500)
		return
	}
	defer rows.Close()
	for rows.Next() {
		q := &QuestionData{}
		var correctOptID sql.NullInt64
		var imgURL sql.NullString
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &correctOptID)
		if correctOptID.Valid {
			q.CorrectOptionID = correctOptID.Int64
		}
		if imgURL.Valid {
			q.ImageURL = imgURL.String
		}
		questionsMap[q.ID] = q
		questionOrder = append(questionOrder, q)
	}
	optRows, err := db.Query("SELECT id, question_id, text, color_hex FROM options")
	if err == nil {
		defer optRows.Close()
		for optRows.Next() {
			var qID int
			o := OptionData{}
			optRows.Scan(&o.ID, &qID, &o.Text, &o.ColorHex)
			if q, ok := questionsMap[qID]; ok {
				q.Options = append(q.Options, o)
			}
		}
	}
	if user != nil {
		predRows, err := db.Query("SELECT question_id, selected_option_id, text_input FROM predictions WHERE user_id = ?", user.ID)
		if err == nil {
			defer predRows.Close()
			for predRows.Next() {
				var qID int
				var selID sql.NullInt64
				var txtInput sql.NullString
				predRows.Scan(&qID, &selID, &txtInput)
				if q, ok := questionsMap[qID]; ok {
					pred := &PredictionData{}
					if selID.Valid {
						pred.SelectedOptionID = selID.Int64
					}
					if txtInput.Valid {
						pred.TextInput = txtInput.String
					}
					q.UserPrediction = pred
					if q.Type == "select" && selID.Valid {
						for i := range q.Options {
							if int64(q.Options[i].ID) == selID.Int64 {
								q.Options[i].IsSelected = true
							}
						}
					}
				}
			}
		}
	}
	var grouped []CategoryGroup
	groupMap := make(map[string]int)
	for _, q := range questionOrder {
		idx, exists := groupMap[q.Category]
		if !exists {
			slug := strings.ReplaceAll(strings.ToLower(q.Category), " ", "-")
			newGroup := CategoryGroup{Name: q.Category, Anchor: slug, Questions: []QuestionData{*q}}
			grouped = append(grouped, newGroup)
			groupMap[q.Category] = len(grouped) - 1
		} else {
			grouped[idx].Questions = append(grouped[idx].Questions, *q)
		}
	}
	render(w, "index.html", PageData{User: user, Categories: grouped, GameStatus: getGameStatus(), RoomAliases: AllowedRooms, ActivePage: "home"})
}

func handleLeaderboardView(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	if user == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	render(w, "leaderboard.html", PageData{User: user, GameStatus: getGameStatus(), RoomAliases: AllowedRooms, ActivePage: "leaderboard"})
}

func handleUserProfile(w http.ResponseWriter, r *http.Request) {
	currentUser := getUser(r)
	if currentUser == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	pathID := r.URL.Path[len("/user/"):]
	targetID, err := strconv.Atoi(pathID)
	if err != nil {
		http.Error(w, "Invalid ID", 400)
		return
	}
	gameStatus := getGameStatus()
	if gameStatus == "OPEN" && currentUser.ID != targetID && !currentUser.IsAdmin {
		render(w, "profile.html", PageData{User: currentUser, GameStatus: gameStatus, TargetUser: nil, ActivePage: "profile"})
		return
	}
	targetUser := &User{ID: targetID}
	var isAdminInt int
	err = db.QueryRow("SELECT username, total_score, room_code, is_admin, icon, color_hex FROM users WHERE id = ?", targetID).Scan(&targetUser.Username, &targetUser.TotalScore, &targetUser.RoomCode, &isAdminInt, &targetUser.Icon, &targetUser.ColorHex)
	if err != nil {
		http.Error(w, "User not found", 404)
		return
	}
	targetUser.IsAdmin = (isAdminInt == 1)
	questionsMap := make(map[int]*QuestionData)
	var questionOrder []*QuestionData
	rows, _ := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id, correct_text_input FROM questions ORDER BY id ASC")
	defer rows.Close()
	for rows.Next() {
		q := &QuestionData{}
		var cID sql.NullInt64
		var imgURL sql.NullString
		var correctText sql.NullString
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &cID, &correctText)
		if cID.Valid {
			q.CorrectOptionID = cID.Int64
		}
		if imgURL.Valid {
			q.ImageURL = imgURL.String
		}
		if correctText.Valid {
			q.CorrectTextInput = correctText.String
		}
		questionsMap[q.ID] = q
		questionOrder = append(questionOrder, q)
	}
	oRows, _ := db.Query("SELECT id, question_id, text FROM options")
	defer oRows.Close()
	for oRows.Next() {
		var qID int
		o := OptionData{}
		oRows.Scan(&o.ID, &qID, &o.Text)
		if q, ok := questionsMap[qID]; ok {
			q.Options = append(q.Options, o)
		}
	}
	pRows, _ := db.Query("SELECT question_id, selected_option_id, text_input FROM predictions WHERE user_id=?", targetID)
	defer pRows.Close()
	for pRows.Next() {
		var qID int
		var sID sql.NullInt64
		var txt sql.NullString
		pRows.Scan(&qID, &sID, &txt)
		if q, ok := questionsMap[qID]; ok {
			pred := &PredictionData{}
			if sID.Valid {
				pred.SelectedOptionID = sID.Int64
			}
			if txt.Valid {
				pred.TextInput = txt.String
			}
			if q.Status == "RESOLVED" && q.Type == "select" {
				pred.IsCorrect = (pred.SelectedOptionID == q.CorrectOptionID)
			}
			q.UserPrediction = pred
		}
	}
	var finalQuestions []QuestionData
	for _, q := range questionOrder {
		finalQuestions = append(finalQuestions, *q)
	}
	render(w, "profile.html", struct {
		User        *User
		TargetUser  *User
		Questions   []QuestionData
		GameStatus  string
		RoomAliases map[string]string
		ActivePage  string
	}{currentUser, targetUser, finalQuestions, gameStatus, AllowedRooms, "profile"})
}

func handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	user := getUser(r)
	if user == nil {
		http.Redirect(w, r, "/", 302)
		return
	}

	targetID := user.ID

	// If Admin AND target_id is provided, update that ID instead
	if user.IsAdmin {
		if tVal := r.FormValue("target_id"); tVal != "" {
			if tID, err := strconv.Atoi(tVal); err == nil {
				targetID = tID
			}
		}
	}

	if r.FormValue("username") != "" {
		newUsername := strings.TrimSpace(r.FormValue("username"))
		if usernameRegex.MatchString(newUsername) {
			db.Exec("UPDATE users SET username = ? WHERE id = ?", newUsername, targetID)
		}
	}
	if r.FormValue("icon") != "" {
		newIcon := r.FormValue("icon")
		newColor := r.FormValue("color")
		match, _ := regexp.MatchString(`^#[0-9a-fA-F]{6}$`, newColor)
		if match {
			db.Exec("UPDATE users SET icon = ?, color_hex = ? WHERE id = ?", newIcon, newColor, targetID)
		}
	}
	if r.Form.Has("room_codes") {
		rawRooms := r.FormValue("room_codes")
		var validatedRooms []string
		parts := strings.Split(rawRooms, ",")
		for _, p := range parts {
			clean := strings.ToUpper(strings.TrimSpace(p))
			if _, ok := AllowedRooms[clean]; ok {
				validatedRooms = append(validatedRooms, clean)
			}
		}
		if len(validatedRooms) == 0 && strings.TrimSpace(rawRooms) != "" {
			ref := r.Header.Get("Referer")
			if ref == "" {
				ref = "/"
			}
			sep := "?"
			if strings.Contains(ref, "?") {
				sep = "&"
			}
			http.Redirect(w, r, ref+sep+"error=invalid_code", 302)
			return
		}
		finalRoomCode := strings.Join(validatedRooms, ",")
		if finalRoomCode == "" {
			finalRoomCode = "GLOBAL"
		}
		db.Exec("UPDATE users SET room_code = ? WHERE id = ?", finalRoomCode, targetID)
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/"
	}
	ref = strings.ReplaceAll(ref, "error=invalid_code", "")
	ref = strings.TrimSuffix(ref, "?")
	ref = strings.TrimSuffix(ref, "&")
	http.Redirect(w, r, ref, 302)
}

func handleLeaderboardAPI(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	scope := r.URL.Query().Get("scope")

	var allCats []string
	catRows, err := db.Query("SELECT DISTINCT category FROM questions WHERE category IS NOT NULL AND category != '' AND category != 'Tie Breaker' ORDER BY category ASC")
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var c string
			catRows.Scan(&c)
			allCats = append(allCats, c)
		}
	} else {
		log.Printf("[LeaderboardAPI] Error fetching categories: %v", err)
	}

	var actualLeft, actualRight int
	var actualTotal int
	var gameResolved bool

	rowsScore, err := db.Query("SELECT type, correct_text_input FROM questions WHERE type IN ('scoreboard-left', 'scoreboard-right') AND status = 'RESOLVED'")
	if err == nil {
		defer rowsScore.Close()
		resolvedCount := 0
		for rowsScore.Next() {
			var qType, valStr string
			rowsScore.Scan(&qType, &valStr)
			val, _ := strconv.Atoi(valStr)
			if qType == "scoreboard-left" {
				actualLeft = val
			} else {
				actualRight = val
			}
			resolvedCount++
		}
		if resolvedCount == 2 {
			actualTotal = actualLeft + actualRight
			gameResolved = true
		}
	}

	// 3. Fetch Users
	query := `SELECT id, username, total_score, room_code, icon, color_hex FROM users`
	var args []interface{}
	if scope != "global" && room != "" {
		query += " WHERE room_code LIKE ?"
		args = append(args, "%"+room+"%")
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("[LeaderboardAPI] DB Error: %v", err)
		http.Error(w, "DB Error", 500)
		return
	}
	defer rows.Close()

	var entries []*LeaderboardEntry
	entryMap := make(map[int]*LeaderboardEntry)

	for rows.Next() {
		e := &LeaderboardEntry{CategoryScores: make(map[string]int)}
		for _, cat := range allCats {
			e.CategoryScores[cat] = 0
		}
		rows.Scan(&e.ID, &e.Username, &e.TotalScore, &e.RoomCode, &e.Icon, &e.ColorHex)
		entries = append(entries, e)
		entryMap[e.ID] = e
	}

	// 4. Fetch User Scoreboard Predictions & Assign TBLeft/TBRight
	predQuery := `
        SELECT p.user_id, q.type, p.text_input 
        FROM predictions p 
        JOIN questions q ON p.question_id = q.id 
        WHERE q.type IN ('scoreboard-left', 'scoreboard-right')
    `
	pRows, err := db.Query(predQuery)
	if err == nil {
		defer pRows.Close()
		userPicks := make(map[int]map[string]int)

		for pRows.Next() {
			var uID int
			var qType, valStr string
			pRows.Scan(&uID, &qType, &valStr)

			if _, exists := userPicks[uID]; !exists {
				userPicks[uID] = make(map[string]int)
			}
			val, _ := strconv.Atoi(valStr)
			userPicks[uID][qType] = val
		}

		// Assign calculated TieBreaker
		for id, picks := range userPicks {
			if entry, ok := entryMap[id]; ok {
				left := picks["scoreboard-left"]
				right := picks["scoreboard-right"]

				// Assign specific scores
				entry.TBLeft = left
				entry.TBRight = right

				// Keep Display String (optional fallback)
				entry.TieBreaker = strconv.Itoa(left + right)
			}
		}

		// Sorting Logic
		sort.Slice(entries, func(i, j int) bool {
			u1 := entries[i]
			u2 := entries[j]
			if u1.TotalScore != u2.TotalScore {
				return u1.TotalScore > u2.TotalScore
			}
			if gameResolved {
				p1 := userPicks[u1.ID]
				p2 := userPicks[u2.ID]
				total1 := p1["scoreboard-left"] + p1["scoreboard-right"]
				total2 := p2["scoreboard-left"] + p2["scoreboard-right"]
				diff1 := abs(total1 - actualTotal)
				diff2 := abs(total2 - actualTotal)
				if diff1 != diff2 {
					return diff1 < diff2
				}
				err1 := abs(p1["scoreboard-left"]-actualLeft) + abs(p1["scoreboard-right"]-actualRight)
				err2 := abs(p2["scoreboard-left"]-actualLeft) + abs(p2["scoreboard-right"]-actualRight)
				if err1 != err2 {
					return err1 < err2
				}
			}
			return u1.Username < u2.Username
		})
	}

	// 5. Populate Actual Category Scores
	catQuery := `
        SELECT p.user_id, q.category, COUNT(*) 
        FROM predictions p 
        JOIN questions q ON p.question_id = q.id 
        WHERE UPPER(q.status) = 'RESOLVED' 
          AND (
            (COALESCE(q.type, 'select') = 'select' AND p.selected_option_id = q.correct_option_id) 
            OR 
            (COALESCE(q.type, 'select') != 'select' AND p.text_input = q.correct_text_input)
          )
        GROUP BY p.user_id, q.category
    `
	cRows, err := db.Query(catQuery)
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var uID int
			var cat string
			var score int
			cRows.Scan(&uID, &cat, &score)
			if entry, ok := entryMap[uID]; ok {
				// "Tie Breaker" cat is already excluded from the map keys in Step 1,
				// Tie Breaker cat is already excluded from the map keys, so strictly skip safely
				if _, exists := entry.CategoryScores[cat]; exists {
					entry.CategoryScores[cat] = score
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func handleGameStatusAPI(w http.ResponseWriter, r *http.Request) {
	status := getGameStatus()
	var count int
	db.QueryRow("SELECT COUNT(*) FROM questions WHERE status = 'RESOLVED'").Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         status,
		"resolved_count": count,
	})
}

func handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "405", 405)
		return
	}
	if getGameStatus() == "LOCKED" {
		http.Error(w, "Locked", 403)
		return
	}
	user := getUser(r)
	if user == nil {
		http.Error(w, "Auth", 401)
		return
	}
	var req PredictionRequest
	json.NewDecoder(r.Body).Decode(&req)
	var optID interface{} = nil
	if req.OptionID != "" {
		optID = req.OptionID
	}
	var txtInput interface{} = nil
	if req.TextInput != "" {
		txtInput = req.TextInput
	}
	db.Exec(`INSERT INTO predictions (user_id, question_id, selected_option_id, text_input) VALUES (?, ?, ?, ?) ON CONFLICT(user_id, question_id) DO UPDATE SET selected_option_id=excluded.selected_option_id, text_input=excluded.text_input`, user.ID, req.QuestionID, optID, txtInput)
	w.WriteHeader(200)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", 302)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	if !usernameRegex.MatchString(username) || len(username) > 20 {
		http.Redirect(w, r, "/?error=invalid_username", 302)
		return
	}
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
		res, _ := db.Exec("INSERT INTO users (username, pin_hash, room_code) VALUES (?, ?, ?)", username, pin, "")
		id, _ := res.LastInsertId()
		userID = int(id)
	}
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{Name: "user_id", Value: strconv.Itoa(userID), Expires: time.Now().Add(24 * 72 * time.Hour), Path: "/", HttpOnly: true, Secure: isSecure})
	http.Redirect(w, r, "/", 302)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "user_id", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/", 302)
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	currentUser := getUser(r)
	var questions []QuestionData
	rows, _ := db.Query("SELECT id, text, category, status, type, image_url, correct_option_id, correct_text_input FROM questions ORDER BY id ASC")
	defer rows.Close()
	for rows.Next() {
		q := QuestionData{}
		var c sql.NullInt64
		var imgURL sql.NullString
		var correctText sql.NullString
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &imgURL, &c, &correctText)
		if c.Valid {
			q.CorrectOptionID = c.Int64
		}
		if imgURL.Valid {
			q.ImageURL = imgURL.String
		}
		if correctText.Valid {
			q.CorrectTextInput = correctText.String
		}
		oRows, _ := db.Query("SELECT id, text FROM options WHERE question_id=?", q.ID)
		for oRows.Next() {
			o := OptionData{}
			oRows.Scan(&o.ID, &o.Text)
			q.Options = append(q.Options, o)
		}
		questions = append(questions, q)
	}
	var users []User
	uRows, err := db.Query("SELECT id, username, room_code, total_score, is_admin, icon, color_hex FROM users ORDER BY username ASC")
	if err != nil {
		log.Println("Error fetching users:", err)
	} else {
		defer uRows.Close()
		for uRows.Next() {
			u := User{}
			var isAdminInt int
			uRows.Scan(&u.ID, &u.Username, &u.RoomCode, &u.TotalScore, &isAdminInt, &u.Icon, &u.ColorHex)
			u.IsAdmin = (isAdminInt == 1)
			users = append(users, u)
		}
	}
	render(w, "admin.html", struct {
		User        *User
		Questions   []QuestionData
		GameStatus  string
		Users       []User
		RoomAliases map[string]string
		ActivePage  string
	}{currentUser, questions, getGameStatus(), users, AllowedRooms, "admin"})
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
		if req.OptionID != "" {
			optID = req.OptionID
		}
		var txtVal interface{} = nil
		if req.TextInput != "" {
			txtVal = req.TextInput
		}
		db.Exec("UPDATE questions SET status='RESOLVED', correct_option_id=?, correct_text_input=? WHERE id=?", optID, txtVal, req.QuestionID)
	}
	db.Exec(`UPDATE users SET total_score = (
		SELECT COUNT(*) FROM predictions p 
		JOIN questions q ON p.question_id = q.id 
		WHERE p.user_id = users.id 
		  AND q.status = 'RESOLVED' 
		  AND (
			(COALESCE(q.type, 'select') = 'select' AND p.selected_option_id = q.correct_option_id)
			OR
			(COALESCE(q.type, 'select') != 'select' AND p.text_input = q.correct_text_input)
		  )
	)`)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGameState(w http.ResponseWriter, r *http.Request) {
	status := r.FormValue("status")
	if status == "OPEN" || status == "LOCKED" {
		setGameStatus(status)
	}
	http.Redirect(w, r, "/admin", 302)
}

func handleAdminRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	file, err := os.ReadFile("questions.json")
	if err != nil {
		http.Redirect(w, r, "/admin?error=read_failed", 302)
		return
	}
	var fileQuestions []SeedQuestion
	if err := json.Unmarshal(file, &fileQuestions); err != nil {
		http.Redirect(w, r, "/admin?error=parse_failed", 302)
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
		qType := q.Type
		if qType == "" {
			qType = "select"
		}
		if id, exists := dbQuestions[q.Text]; exists {
			db.Exec("UPDATE questions SET category=?, type=?, image_url=? WHERE id=?", q.Category, qType, q.ImageURL, id)
			for _, o := range q.Options {
				var optID int
				err := db.QueryRow("SELECT id FROM options WHERE question_id = ? AND text = ?", id, o.Text).Scan(&optID)
				if err == nil {
					db.Exec("UPDATE options SET color_hex = ? WHERE id = ?", o.Color, optID)
				} else {
					db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", id, o.Text, o.Color)
				}
			}
		} else {
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
	http.Redirect(w, r, "/admin?status=refreshed", 302)
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	action := r.FormValue("action")
	userIDStr := r.FormValue("user_id")
	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "Invalid User ID", 400)
		return
	}
	switch action {
	case "reset_pin":
		db.Exec("UPDATE users SET pin_hash = '' WHERE id = ?", userID)
	case "delete":
		db.Exec("DELETE FROM predictions WHERE user_id = ?", userID)
		db.Exec("DELETE FROM users WHERE id = ?", userID)
	case "update_room":
		r.ParseForm()
		rawRooms := r.Form["room_code"]
		var validatedRooms []string
		for _, p := range rawRooms {
			clean := strings.ToUpper(strings.TrimSpace(p))
			if _, ok := AllowedRooms[clean]; ok {
				validatedRooms = append(validatedRooms, clean)
			}
		}
		finalRoom := strings.Join(validatedRooms, ",")
		if finalRoom == "" {
			finalRoom = "GLOBAL"
		}
		db.Exec("UPDATE users SET room_code = ? WHERE id = ?", finalRoom, userID)
	case "update_username":
		newUsername := strings.TrimSpace(r.FormValue("new_username"))
		if newUsername != "" && usernameRegex.MatchString(newUsername) {
			db.Exec("UPDATE users SET username = ? WHERE id = ?", newUsername, userID)
		}
	}
	http.Redirect(w, r, "/admin", 302)
}

func handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat("backups"); os.IsNotExist(err) {
		os.Mkdir("backups", 0755)
	}
	dbPath := os.Getenv("DB_NAME")
	if dbPath == "" {
		dbPath = "./game.db"
	}
	sourceFile, err := os.Open(dbPath)
	if err != nil {
		http.Error(w, "Failed to open DB", 500)
		return
	}
	defer sourceFile.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=game_backup_%s.db", time.Now().Format("20060102_150405")))
	w.Header().Set("Content-Type", "application/x-sqlite3")
	io.Copy(w, sourceFile)
}

func handleResults(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)


	
	// Fetch all questions and their correct answers
	questionsMap := make(map[int]*QuestionData)
	var questionOrder []*QuestionData
	
	// Only fetch necessary fields
	rows, err := db.Query("SELECT id, text, category, status, type, correct_option_id, correct_text_input FROM questions ORDER BY id ASC")
	if err != nil {
		http.Error(w, "DB Error", 500)
		return
	}
	defer rows.Close()

	for rows.Next() {
		q := &QuestionData{}
		var correctOptID sql.NullInt64
		var correctText sql.NullString
		rows.Scan(&q.ID, &q.Text, &q.Category, &q.Status, &q.Type, &correctOptID, &correctText)
		
		if correctOptID.Valid { q.CorrectOptionID = correctOptID.Int64 }
		if correctText.Valid { q.CorrectTextInput = correctText.String }
		
		questionsMap[q.ID] = q
		questionOrder = append(questionOrder, q)
	}

	// Fetch options to display the text of the winning option
	optRows, err := db.Query("SELECT id, question_id, text, color_hex FROM options")
	if err == nil {
		defer optRows.Close()
		for optRows.Next() {
			var qID int
			o := OptionData{}
			optRows.Scan(&o.ID, &qID, &o.Text, &o.ColorHex)
			if q, ok := questionsMap[qID]; ok {
				q.Options = append(q.Options, o)
			}
		}
	}

	render(w, "results.html", PageData{User: user, Categories: nil, GameStatus: getGameStatus(), ActivePage: "results", Questions: extractQuestions(questionOrder)})
}

func extractQuestions(qs []*QuestionData) []QuestionData {
	var res []QuestionData
	for _, q := range qs {
		res = append(res, *q)
	}
	return res
}

func handleAdminEditQuestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	
	idStr := r.FormValue("id")
	text := r.FormValue("text")
	category := r.FormValue("category")
	
	if idStr == "new" {
		// Create New
		db.Exec("INSERT INTO questions (text, category, status, type) VALUES (?, ?, 'OPEN', 'select')", text, category)
	} else {
		// Update Existing
		db.Exec("UPDATE questions SET text = ?, category = ? WHERE id = ?", text, category, idStr)
	}
	http.Redirect(w, r, "/admin", 302)
}

func handleAdminDeleteQuestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	id := r.FormValue("id")
	
	// Cleanup dependencies
	db.Exec("DELETE FROM predictions WHERE question_id = ?", id)
	db.Exec("DELETE FROM options WHERE question_id = ?", id)
	db.Exec("DELETE FROM questions WHERE id = ?", id)
	
	http.Redirect(w, r, "/admin", 302)
}

func handleAdminEditOption(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	
	qID := r.FormValue("question_id")
	optID := r.FormValue("id")
	text := r.FormValue("text")
	color := r.FormValue("color")
	
	if optID == "new" {
		db.Exec("INSERT INTO options (question_id, text, color_hex) VALUES (?, ?, ?)", qID, text, color)
	} else {
		db.Exec("UPDATE options SET text = ?, color_hex = ? WHERE id = ?", text, color, optID)
	}
	http.Redirect(w, r, "/admin", 302)
}

func handleAdminDeleteOption(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "405", 405); return }
	id := r.FormValue("id")
	db.Exec("DELETE FROM options WHERE id = ?", id)
	http.Redirect(w, r, "/admin", 302)
}

func render(w http.ResponseWriter, tmpl string, data interface{}) {
	if err := templates.ExecuteTemplate(w, tmpl, data); err != nil {
		log.Println(err)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
