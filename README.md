# HTTPCache

HTTPCache is a simple yet flexible HTTP caching client library for Go, featuring regex-based URL caching policies.

## Features

- Persistent cache storage using LevelDB
- Configurable caching policies
- URL pattern matching using regular expressions
- Thread-safe singleton implementation
- Configurable cache expiration times

## Installation

```bash
go get github.com/crawlerclub/httpcache
```

## Usage

### Basic Example

```go
import "github.com/crawlerclub/httpcache"

func main() {
    // Get a singleton HTTP client instance
    client := httpcache.GetClient()
    defer client.Close()

    // Make HTTP requests - responses will be cached based on policies
    data, err := client.Get("https://api.example.com/data")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(string(data))
}
```

### Cache Policy Configuration

Create a policy file (default location: .httpcache/policies.txt) with one policy per line in the format: `regex=duration`

```text
# Example policy file
.*\.example\.com=5m        # Cache example.com requests for 5 minutes
.*\.api\.com/v1/.*=1h     # Cache api.com/v1/ requests for 1 hour
.*=10m                    # Default cache duration of 10 minutes
```

Supported time units:
- `s`: seconds
- `m`: minutes
- `h`: hours
- `d`: days

### Command Line Flags

```bash
-cache_dir string
    Directory for cache storage (default ".httpcache")
-policies_file string
    Path to cache policies file (default ".httpcache/policies.txt")
```

## Advanced Example

```go
package main

import (
    "flag"
    "log"
    "github.com/yourusername/httpcache"
)

func main() {
    flag.Parse()

    client := httpcache.GetClient()
    defer client.Close()

    urls := []string{
        "https://api.example.com/data",
        "https://api.test.com/v1/users",
    }

    for _, url := range urls {
        data, err := client.Get(url)
        if err != nil {
            log.Printf("Error fetching %s: %v", url, err)
            continue
        }
        log.Printf("Response from %s: %s", url, string(data))
    }
}
```

## Policy Examples

Here are some common caching policy configurations:

```text
# API caching policies
.*\/api\/v1\/users=5m           # Cache user data for 5 minutes
.*\/api\/v1\/products=1h        # Cache product data for 1 hour
.*\/static\/.*=24h              # Cache static resources for 24 hours
.*\/feed\.json=30m              # Cache feed data for 30 minutes
```

## Important Notes

1. Cache policies are matched in order - first match wins
2. Specify a custom cache directory in production environments
3. Ensure write permissions for the cache directory
4. Uses Go's standard library regex implementation

## Thread Safety

The library implements a thread-safe singleton pattern, making it safe to use across multiple goroutines.

## Error Handling

The library provides proper error handling for:
- Network failures
- Invalid URLs
- Cache storage issues
- Policy configuration errors

## Dependencies

- github.com/liuzl/store - For LevelDB storage implementation

## License

MIT License

## Contributing

Issues and Pull Requests are welcome!

## Testing

Run the test suite:

```bash
go test -v
```

The test suite includes coverage for:
- Basic HTTP requests
- Cache hits and misses
- Policy matching
- Cache expiration
- Error scenarios
