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
	FinalURL  string    `json:"final_url"`
	CrawledAt time.Time `json:"crawled_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CachePolicy struct {
	Pattern *regexp.Regexp
	TTL     time.Duration
}

type Cache struct {
	Store    *store.LevelStore
	Policies []CachePolicy
}

type HTTPClient struct {
	cache  *Cache
	client *http.Client
}

var (
	instance *HTTPClient
	once     sync.Once
)

func LoadPoliciesFromFile(filename string) ([]CachePolicy, error) {
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
		policies, err := LoadPoliciesFromFile(*policiesFile)
		if err != nil {
			log.Fatalf("Failed to load cache policies: %v", err)
		}

		store, err := store.NewLevelStore(*cacheDir + "/data")
		if err != nil {
			log.Fatalf("Failed to initialize cache: %v", err)
		}
		instance = &HTTPClient{
			cache: &Cache{
				Store:    store,
				Policies: policies,
			},
			client: &http.Client{},
		}
	})

	if instance == nil {
		log.Fatal("Failed to initialize HTTPClient")
	}
	return instance
}

func (c *Cache) GetTTL(url string) time.Duration {
	for _, policy := range c.Policies {
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

type ContentValidator func([]byte) bool

func (hc *HTTPClient) GetWithValidator(url string, validator ContentValidator) ([]byte, string, error) {
	key := hashKey(url)

	ttl := hc.cache.GetTTL(url)
	if ttl > 0 {
		if value, finalURL, found := hc.cache.Get(key); found {
			if validator == nil || validator(value) {
				return value, finalURL, nil
			}
			// invalid cache, delete it
			_ = hc.cache.Delete(key)
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", useragent.UserAgents[0].String())
	resp, err := hc.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, finalURL, err
	}

	shouldCache := true
	if validator != nil {
		shouldCache = validator(body)
	}

	if shouldCache && ttl > 0 {
		hc.cache.Set(key, body, url, finalURL, ttl)
	}

	return body, finalURL, nil
}

func (hc *HTTPClient) Get(url string) ([]byte, error) {
	data, _, err := hc.GetWithValidator(url, nil)
	return data, err
}

func (hc *HTTPClient) GetWithFinalURL(url string) ([]byte, string, error) {
	return hc.GetWithValidator(url, nil)
}

func (c *Cache) Get(key string) ([]byte, string, bool) {
	value, err := c.Store.Get(key)
	if err != nil || value == nil {
		return nil, "", false
	}

	var entry CacheEntry
	if err := store.BytesToObject(value, &entry); err != nil {
		return nil, "", false
	}

	// Check if entry has expired
	now := time.Now()
	isExpired := false

	// If CrawledAt is set (not zero time), use it with the matching policy TTL
	if !entry.CrawledAt.IsZero() {
		ttl := c.GetTTL(entry.URL)
		if now.Sub(entry.CrawledAt) > ttl {
			isExpired = true
		}
	} else {
		// Backward compatibility: use ExpiresAt for older entries
		isExpired = now.After(entry.ExpiresAt)
	}

	if isExpired {
		_ = c.Store.Delete(key)
		return nil, "", false
	}

	return entry.Data, entry.FinalURL, true
}

func (c *Cache) Set(key string, data []byte, url string, finalURL string, ttl time.Duration) {
	now := time.Now()
	entry := CacheEntry{
		Data:      data,
		URL:       url,
		FinalURL:  finalURL,
		CrawledAt: now,
		ExpiresAt: now.Add(ttl),
	}

	encoded, err := store.ObjectToBytes(entry)
	if err != nil {
		log.Printf("Failed to encode cache entry: %v", err)
		return
	}

	if err := c.Store.Put(key, encoded); err != nil {
		log.Printf("Failed to store cache entry: %v", err)
	}
}

func (hc *HTTPClient) Close() {
	if err := hc.cache.Store.Close(); err != nil {
		log.Printf("Failed to close cache: %v", err)
	}
	instance = nil
	once = sync.Once{}
}

// NewClient creates a new HTTPClient instance with custom policies and cache directory
func NewClient(cacheDir string, policies []CachePolicy) (*HTTPClient, error) {
	if cacheDir == "" {
		return nil, fmt.Errorf("cache directory is required")
	}

	store, err := store.NewLevelStore(cacheDir + "/data")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache: %+v", err)
	}

	return &HTTPClient{
		cache: &Cache{
			Store:    store,
			Policies: policies,
		},
		client: &http.Client{},
	}, nil
}

func (hc *HTTPClient) Fetch(url string, validator ContentValidator) ([]byte, error) {
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

	ttl := hc.cache.GetTTL(url)
	if ttl > 0 {
		shouldCache := true
		if validator != nil {
			shouldCache = validator(body)
		}

		if shouldCache {
			key := hashKey(url)
			hc.cache.Set(key, body, url, "", ttl)
		}
	}

	return body, nil
}

// Delete removes an entry from the cache
func (c *Cache) Delete(key string) error {
	return c.Store.Delete(key)
}

// DeleteURL removes the cached entry for the given URL
func (hc *HTTPClient) DeleteURL(url string) error {
	key := hashKey(url)
	return hc.cache.Delete(key)
}

func (hc *HTTPClient) FetchWithFinalURL(url string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", useragent.UserAgents[0].String())
	resp, err := hc.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, finalURL, err
	}

	ttl := hc.cache.GetTTL(url)
	if ttl > 0 {
		key := hashKey(url)
		hc.cache.Set(key, body, url, finalURL, ttl)
	}

	return body, finalURL, nil
}

func (hc *HTTPClient) GetStore() *store.LevelStore {
	return hc.cache.Store
}
