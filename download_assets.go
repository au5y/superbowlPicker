package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

func main() {
	// Ensure directory exists
	assetsDir := "static/assets"
	if _, err := os.Stat(assetsDir); os.IsNotExist(err) {
		os.MkdirAll(assetsDir, 0755)
	}

	teams := []string{
		"ari", "atl", "bal", "buf", "car", "chi", "cin", "cle", "dal", "den", "det", "gb", "hou", "ind", "jax", "kc", "lv", "lac", "lar", "mia", "min", "ne", "no", "nyg", "nyj", "phi", "pit", "sf", "sea", "tb", "ten", "was",
	}

	var wg sync.WaitGroup

	fmt.Println("🏈 Starting Asset Download...")

	// 1. Download Team Logos
	for _, team := range teams {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			url := fmt.Sprintf("https://a.espncdn.com/i/teamlogos/nfl/500/%s.png", t)
			filename := fmt.Sprintf("%s.png", t)
			downloadFile(url, filepath.Join(assetsDir, filename))
		}(team)
	}

	// 2. Download Coins (Heads/Tails)
	coins := map[string]string{
		"heads.png": "https://upload.wikimedia.org/wikipedia/commons/thumb/a/a0/2006_Quarter_Proof.png/200px-2006_Quarter_Proof.png",
		"tails.png": "https://washington-quarters.com/coins/nevada-reverse.png",
	}

	for name, url := range coins {
		wg.Add(1)
		go func(n, u string) {
			defer wg.Done()
			downloadFile(u, filepath.Join(assetsDir, n))
		}(name, url)
	}

	wg.Wait()
	fmt.Println("✅ All assets downloaded to /static/assets/")
}

func downloadFile(url string, dest string) {
	// Check if exists first to save bandwidth
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("   Skipping %s (already exists)\n", dest)
		return
	}

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("❌ Failed to download %s: %v\n", url, err)
		return
	}
	defer resp.Body.Close()

	out, err := os.Create(dest)
	if err != nil {
		fmt.Printf("❌ Failed to create file %s: %v\n", dest, err)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to save %s: %v\n", dest, err)
		return
	}

	fmt.Printf("   Downloaded %s\n", dest)
}
