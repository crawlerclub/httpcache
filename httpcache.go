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
	"github.com/projectdiscovery/useragent"
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
	defaultPolicy := CachePolicy{
		Pattern: regexp.MustCompile(".*"),
		TTL:     10 * time.Minute,
	}

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
		// Skip empty lines and comment-only lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Remove inline comments
		if idx := strings.Index(line, "#"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		// Split on last = character
		idx := strings.LastIndex(line, "=")
		if idx == -1 {
			return nil, fmt.Errorf("invalid policy format: %s", line)
		}
		pattern := strings.TrimSpace(line[:idx])
		duration := strings.TrimSpace(line[idx+1:])

		// Compile pattern
		compiledPattern, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %s", err)
		}

		// Parse duration
		parsedDuration, err := time.ParseDuration(duration)
		if err != nil {
			return nil, fmt.Errorf("invalid duration: %s", err)
		}

		policies = append(policies, CachePolicy{
			Pattern: compiledPattern,
			TTL:     parsedDuration,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading policies file: %v", err)
	}

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
			return value, nil
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", useragent.UserAgents[0].String())
	resp, err := hc.client.Do(req)
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
	instance = nil
	once = sync.Once{}
}
