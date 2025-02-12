package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/liuzl/store"
)

var (
	cacheDir = flag.String("cache_dir", ".httpcache", "Directory for HTTP cache storage")
	url      = flag.String("url", "", "URL to check in cache")
	outfile  = flag.String("outfile", "", "Output file to save the cache content")
)

type CacheEntry struct {
	Data      []byte    `json:"data"`
	URL       string    `json:"url"`
	FinalURL  string    `json:"final_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func hashKey(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])
}

func printCacheEntry(key string, entry *CacheEntry) {
	fmt.Printf("Cache Key: %s\n", key)
	fmt.Printf("Original URL: %s\n", entry.URL)
	if entry.FinalURL != "" && entry.FinalURL != entry.URL {
		fmt.Printf("Final URL: %s\n", entry.FinalURL)
	}
	fmt.Printf("Expires At: %s\n", entry.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("Time Until Expiration: %s\n", time.Until(entry.ExpiresAt).Round(time.Second))
	fmt.Printf("Data Size: %d bytes\n", len(entry.Data))
	fmt.Printf("First 200 bytes of data: %s\n", truncateString(string(entry.Data), 200000))
	fmt.Println(strings.Repeat("-", 80))
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func main() {
	flag.Parse()

	if *url == "" {
		fmt.Println("Please provide a URL to check with -url flag")
		flag.Usage()
		os.Exit(1)
	}

	// Initialize store
	db, err := store.NewLevelStore(*cacheDir + "/data")
	if err != nil {
		log.Fatalf("Failed to initialize cache store: %v", err)
	}
	defer db.Close()

	// Check specific URL
	key := hashKey(*url)
	value, err := db.Get(key)
	if err != nil {
		log.Fatalf("Error reading from cache: %v", err)
	}

	if value == nil {
		fmt.Printf("No cache entry found for URL: %s\n", *url)
		return
	}

	var entry CacheEntry

	if err := store.BytesToObject(value, &entry); err != nil {
		log.Fatalf("Error decoding cache entry: %v", err)
	}

	printCacheEntry(key, &entry)

	// Save to file if outfile is specified
	if *outfile != "" {
		if err := os.WriteFile(*outfile, entry.Data, 0644); err != nil {
			log.Fatalf("Error writing to output file: %v", err)
		}
		fmt.Printf("Cache content saved to: %s\n", *outfile)
	}

	// Check if entry is expired
	if time.Now().After(entry.ExpiresAt) {
		fmt.Println("Note: This cache entry is expired")
	}
}
