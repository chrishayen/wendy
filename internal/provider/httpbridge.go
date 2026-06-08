package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrBackend = errors.New("provider backend error")
	ErrTimeout = errors.New("provider timeout")
)

type InvokeError struct {
	contracts.ErrorObject
	StatusCode int
}

func (e InvokeError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "provider invocation failed"
}

type HTTPBridgeConfig struct {
	Routes         map[string]HTTPBridgeRoute `json:"routes"`
	Client         *http.Client               `json:"-"`
	SecretResolver SecretResolver             `json:"-"`
}

type HTTPBridgeRoute struct {
	URL               string            `json:"url"`
	Method            string            `json:"method,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	HeadersFromEnv    map[string]string `json:"headers_from_env,omitempty"`
	HeadersFromSecret map[string]string `json:"headers_from_secret,omitempty"`
	TimeoutSeconds    int               `json:"timeout_seconds,omitempty"`
}

func NewHTTPBridgeServer(manifest contracts.ProviderManifest, cfg HTTPBridgeConfig) (*Server, error) {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	handlers := map[string]CapabilityHandler{}
	for _, capability := range manifest.Capabilities {
		route, ok := cfg.Routes[capability.ID]
		if !ok {
			return nil, fmt.Errorf("%w: route missing for capability %s", ErrValidation, capability.ID)
		}
		normalized, err := normalizeHTTPBridgeRoute(context.Background(), route, cfg.SecretResolver)
		if err != nil {
			return nil, fmt.Errorf("%w: route %s: %s", ErrValidation, capability.ID, err)
		}
		handlers[capability.ID] = httpBridgeHandler(client, normalized)
	}
	return NewServer(manifest, handlers)
}

func normalizeHTTPBridgeRoute(ctx context.Context, route HTTPBridgeRoute, secretResolver SecretResolver) (HTTPBridgeRoute, error) {
	if route.URL == "" {
		return HTTPBridgeRoute{}, errors.New("url is required")
	}
	parsed, err := url.Parse(route.URL)
	if err != nil {
		return HTTPBridgeRoute{}, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return HTTPBridgeRoute{}, errors.New("url must use http or https")
	}
	if parsed.Host == "" {
		return HTTPBridgeRoute{}, errors.New("url host is required")
	}
	method := strings.ToUpper(route.Method)
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost {
		return HTTPBridgeRoute{}, errors.New("only POST routes are supported")
	}
	route.Method = method
	if route.Headers == nil {
		route.Headers = map[string]string{}
	}
	for header, envName := range route.HeadersFromEnv {
		if header == "" {
			return HTTPBridgeRoute{}, errors.New("headers_from_env header name is required")
		}
		if envName == "" {
			return HTTPBridgeRoute{}, fmt.Errorf("headers_from_env %s env var is required", header)
		}
		value, ok := os.LookupEnv(envName)
		if !ok {
			return HTTPBridgeRoute{}, fmt.Errorf("environment variable %s is not set", envName)
		}
		route.Headers[header] = value
	}
	if err := resolveSecretMap(ctx, secretResolver, "headers_from_secret", route.Headers, route.HeadersFromSecret); err != nil {
		return HTTPBridgeRoute{}, err
	}
	return route, nil
}

func httpBridgeHandler(client *http.Client, route HTTPBridgeRoute) CapabilityHandler {
	return func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
		if route.TimeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(route.TimeoutSeconds)*time.Second)
			defer cancel()
		}
		body, err := json.Marshal(req)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		httpReq, err := http.NewRequestWithContext(ctx, route.Method, route.URL, bytes.NewReader(body))
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")
		for key, value := range route.Headers {
			httpReq.Header.Set(key, value)
		}
		if req.Context.RequestID != "" {
			httpReq.Header.Set("X-Request-ID", req.Context.RequestID)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: provider invocation timed out", ErrTimeout)
			}
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: %s", ErrBackend, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if errObj, ok := decodeHTTPBridgeErrorEnvelope(body); ok {
				return contracts.ProviderInvokeResponse{}, InvokeError{ErrorObject: errObj, StatusCode: resp.StatusCode}
			}
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: HTTP %d: %s", ErrBackend, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return decodeHTTPBridgeResponse(resp.Body)
	}
}

func decodeHTTPBridgeResponse(body io.Reader) (contracts.ProviderInvokeResponse, error) {
	data, err := io.ReadAll(io.LimitReader(body, 10<<20))
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	var envelope struct {
		OK    *bool                  `json:"ok"`
		Data  json.RawMessage        `json:"data"`
		Error *contracts.ErrorObject `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.OK != nil {
		if !*envelope.OK {
			if envelope.Error != nil {
				return contracts.ProviderInvokeResponse{}, InvokeError{ErrorObject: *envelope.Error}
			}
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: backend returned error envelope", ErrBackend)
		}
		var out contracts.ProviderInvokeResponse
		if err := json.Unmarshal(envelope.Data, &out); err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		return out, nil
	}
	var out contracts.ProviderInvokeResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	return out, nil
}

func decodeHTTPBridgeErrorEnvelope(data []byte) (contracts.ErrorObject, bool) {
	var envelope struct {
		OK    *bool                  `json:"ok"`
		Error *contracts.ErrorObject `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.OK == nil || *envelope.OK || envelope.Error == nil {
		return contracts.ErrorObject{}, false
	}
	return *envelope.Error, true
}
