package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
)

// Global templates cache
var templates = template.Must(template.ParseGlob("templates/*.html"))

func main() {
	// 1. Initialize Database
	InitDB("./game.db")
	defer db.Close()

	// 2. Setup Routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/predict", handlePredict) // API endpoint for auto-save
	http.HandleFunc("/admin", handleAdmin)

	// 3. Serve Static Files (CSS, JS)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 4. Start Server
	port := ":8080"
	fmt.Printf("Gridiron Oracle running at http://localhost%s\n", port)
	err := http.ListenAndServe(port, nil)
	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

// -- Handlers (Stubs for now) --

func handleIndex(w http.ResponseWriter, r *http.Request) {
	// TODO: Check session cookie. If no session, show Login. If session, show Dashboard.
	// For now, just render a placeholder
	if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement Login Logic
}

func handlePredict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// TODO: Parse JSON, Update DB (UPSERT)
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	// TODO: Simple Auth Check & Render Admin Panel
}