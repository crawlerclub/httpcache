package httpcache

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	*cacheDir = ".httpcache_test"
	*policiesFile = ".httpcache_test/policies.txt"

	code := m.Run()
	os.RemoveAll(".httpcache_test")
	os.Exit(code)
}

func TestHTTPClientGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	once = sync.Once{}
	client := GetClient()
	defer client.Close()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "Basic GET request",
			url:     server.URL,
			wantErr: false,
		},
		{
			name:    "Invalid URL",
			url:     "http://invalid.url.that.does.not.exist",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data1, err1 := client.Get(tt.url)
			if (err1 != nil) != tt.wantErr {
				t.Errorf("First request: error = %v, wantErr %v", err1, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if string(data1) != "test response" {
				t.Errorf("Unexpected response: got %s, want %s", string(data1), "test response")
			}

			data2, err2 := client.Get(tt.url)
			if err2 != nil {
				t.Errorf("Second request: unexpected error = %v", err2)
				return
			}
			if string(data1) != string(data2) {
				t.Errorf("Cache mismatch: first response %s != second response %s", string(data1), string(data2))
			}
		})
	}
}

func TestCachePolicy(t *testing.T) {
	err := os.MkdirAll(".httpcache_test", 0755)
	if err != nil {
		t.Fatal(err)
	}

	policyContent := `
# Test policy file
.*\.example\.com=5m
.*\.test\.com=1s
`
	err = os.WriteFile(".httpcache_test/policies.txt", []byte(policyContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	once = sync.Once{}
	client := GetClient()
	defer client.Close()

	tests := []struct {
		name    string
		url     string
		wantTTL time.Duration
	}{
		{
			name:    "example.com domain",
			url:     "http://api.example.com/test",
			wantTTL: 5 * time.Minute,
		},
		{
			name:    "test.com domain",
			url:     "http://api.test.com/test",
			wantTTL: 1 * time.Second,
		},
		{
			name:    "default policy",
			url:     "http://other.com/test",
			wantTTL: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl := client.cache.GetTTL(tt.url)
			if ttl != tt.wantTTL {
				t.Errorf("GetTTL() = %v, want %v", ttl, tt.wantTTL)
			}
		})
	}
}

func TestCacheExpiration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	err := os.WriteFile(".httpcache_test/policies.txt", []byte(".*=1s"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	once = sync.Once{}
	client := GetClient()
	defer client.Close()

	data1, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	data2, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}

	if string(data1) != string(data2) {
		t.Errorf("Responses don't match: %s != %s", string(data1), string(data2))
	}
}

func TestHTTPClientRedirect(t *testing.T) {
	// Initialize client
	client := GetClient()

	// Test case
	originalURL := "https://httpbin.org/redirect/1"
	expectedFinalURL := "https://httpbin.org/get"

	// Test GetWithFinalURL
	data, finalURL, err := client.GetWithFinalURL(originalURL)
	if err != nil {
		t.Fatalf("GetWithFinalURL failed: %v", err)
	}

	// Check if final URL matches expected
	if finalURL != expectedFinalURL {
		t.Errorf("Final URL mismatch:\ngot:  %s\nwant: %s", finalURL, expectedFinalURL)
	}

	// Verify that data is not empty
	if len(data) == 0 {
		t.Error("Received empty response body")
	}

	// Test cache behavior
	// Second request should use cache and return same final URL
	cachedData, cachedFinalURL, err := client.GetWithFinalURL(originalURL)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}

	if cachedFinalURL != expectedFinalURL {
		t.Errorf("Cached final URL mismatch:\ngot:  %s\nwant: %s", cachedFinalURL, expectedFinalURL)
	}

	// Verify that both responses match
	if string(data) != string(cachedData) {
		t.Error("Cached response differs from original response")
	}
}
