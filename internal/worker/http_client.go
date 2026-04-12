package worker

import (
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HTTPClient is a custom HTTP client with connection pooling and timeouts
type HTTPClient struct {
	client  *http.Client
	logger  *zap.Logger
	timeout time.Duration
}

// NewHTTPClient creates a new HTTP client
func NewHTTPClient(timeout time.Duration, logger *zap.Logger) *HTTPClient {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		DisableKeepAlives: false,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &HTTPClient{
		client:  client,
		logger:  logger,
		timeout: timeout,
	}
}

// Do performs an HTTP request
func (c *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := c.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		c.logger.Debug("HTTP request failed",
			zap.String("method", req.Method),
			zap.String("url", req.URL.String()),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return nil, err
	}

	c.logger.Debug("HTTP request completed",
		zap.String("method", req.Method),
		zap.String("url", req.URL.String()),
		zap.Int("status", resp.StatusCode),
		zap.Duration("duration", duration),
	)

	return resp, nil
}

// Close closes the HTTP client and cleans up resources
func (c *HTTPClient) Close() {
	if transport, ok := c.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
