package httpcache

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/liuzl/store"
)

var (
	cacheDir     = flag.String("cache_dir", ".httpcache", "Directory for HTTP cache storage")
	policiesFile = flag.String("policies_file", ".httpcache/policies.txt", "File containing cache policies, one per line in format: regex=duration")
)

type CacheEntry struct {
	Data      []byte    `json:"data"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CachePolicy struct {
	Pattern *regexp.Regexp
	TTL     time.Duration
}

type Cache struct {
	store    *store.LevelStore
	policies []CachePolicy
}

type HTTPClient struct {
	cache  *Cache
	client *http.Client
}

var (
	instance *HTTPClient
	once     sync.Once
)

func loadPoliciesFromFile(filename string) ([]CachePolicy, error) {
	// Default policy: cache everything for 10 minutes
	defaultPolicy := CachePolicy{
		Pattern: regexp.MustCompile(".*"),
		TTL:     10 * time.Minute,
	}

	// If no file specified or file doesn't exist, return only default policy
	if filename == "" {
		return []CachePolicy{defaultPolicy}, nil
	}

	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return []CachePolicy{defaultPolicy}, nil
		}
		return nil, fmt.Errorf("failed to open policies file: %v", err)
	}
	defer file.Close()

	policies := []CachePolicy{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid policy format: %s", line)
		}

		pattern, err := regexp.Compile(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %s", err)
		}

		duration, err := time.ParseDuration(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid duration: %s", err)
		}

		policies = append(policies, CachePolicy{
			Pattern: pattern,
			TTL:     duration,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading policies file: %v", err)
	}

	// Add default policy at the end (will be used if no other patterns match)
	policies = append(policies, defaultPolicy)
	return policies, nil
}

func GetClient() *HTTPClient {
	once.Do(func() {
		policies, err := loadPoliciesFromFile(*policiesFile)
		if err != nil {
			log.Fatalf("Failed to load cache policies: %v", err)
		}

		store, err := store.NewLevelStore(*cacheDir + "/data")
		if err != nil {
			log.Fatalf("Failed to initialize cache: %v", err)
		}
		instance = &HTTPClient{
			cache: &Cache{
				store:    store,
				policies: policies,
			},
			client: &http.Client{},
		}
		log.Println("HTTP cache client initialized successfully")
	})

	if instance == nil {
		log.Fatal("Failed to initialize HTTPClient")
	}
	return instance
}

func (c *Cache) getTTL(url string) time.Duration {
	for _, policy := range c.policies {
		if policy.Pattern.MatchString(url) {
			return policy.TTL
		}
	}
	return 0
}

func hashKey(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])
}

func (hc *HTTPClient) Get(url string) ([]byte, error) {
	key := hashKey(url)

	ttl := hc.cache.getTTL(url)
	if ttl > 0 {
		if value, found := hc.cache.Get(key); found {
			fmt.Println("Cache hit!")
			return value, nil
		}
	}

	fmt.Println("Cache miss. Making HTTP request...")
	resp, err := hc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if ttl > 0 {
		hc.cache.Set(key, body, url, ttl)
	}

	return body, nil
}

func (c *Cache) Get(key string) ([]byte, bool) {
	value, err := c.store.Get(key)
	if err != nil || value == nil {
		return nil, false
	}

	var entry CacheEntry
	if err := store.BytesToObject(value, &entry); err != nil {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		_ = c.store.Delete(key)
		return nil, false
	}

	return entry.Data, true
}

func (c *Cache) Set(key string, data []byte, url string, ttl time.Duration) {
	entry := CacheEntry{
		Data:      data,
		URL:       url,
		ExpiresAt: time.Now().Add(ttl),
	}

	encoded, err := store.ObjectToBytes(entry)
	if err != nil {
		log.Printf("Failed to encode cache entry: %v", err)
		return
	}

	if err := c.store.Put(key, encoded); err != nil {
		log.Printf("Failed to store cache entry: %v", err)
	}
}

func (hc *HTTPClient) Close() {
	if err := hc.cache.store.Close(); err != nil {
		log.Printf("Failed to close cache: %v", err)
	}
}

func (c *Cache) findMatchingPolicy(url string) CachePolicy {
	for _, policy := range c.policies {
		if policy.Pattern.MatchString(url) {
			return policy
		}
	}
	return CachePolicy{}
}
