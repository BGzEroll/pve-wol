package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pve-wol/internal/config"
)

const (
	requestTimeout = 20 * time.Second
	maxResponse    = 4 << 20
)

var (
	netKeyPattern = regexp.MustCompile(`^net[0-9]+$`)
	macPattern    = regexp.MustCompile(`(?i)(?:[0-9a-f]{2}:){5}[0-9a-f]{2}`)
)

type Guest struct {
	VMID   int
	Type   string
	Node   string
	Name   string
	Status string
	MACs   []string
}

type Client struct {
	baseURL       string
	authorization string
	httpClient    *http.Client
}

type Resource struct {
	Type   string `json:"type"`
	VMID   int    `json:"vmid"`
	Node   string `json:"node"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("PVE API returned HTTP %d: %s", e.Status, e.Message)
}

func (e *APIError) AlreadyRunning() bool {
	message := strings.ToLower(e.Message)
	return strings.Contains(message, "already running") ||
		strings.Contains(message, "is running") ||
		strings.Contains(message, "already active")
}

func New(cfg config.PVEConfig) (*Client, error) {
	rawURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid pve.url: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("pve.url must be an HTTP(S) URL")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicitly configured for PVE self-signed certificates
	}

	return &Client{
		baseURL:       rawURL,
		authorization: "PVEAPIToken=" + cfg.TokenID + "=" + cfg.TokenSecret,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
		},
	}, nil
}

func (c *Client) URL() string {
	return c.baseURL
}

func (c *Client) Sync(ctx context.Context) ([]Guest, error) {
	var resources []Resource
	if err := c.get(ctx, "/cluster/resources?type=vm", &resources); err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}

	guests := make([]Guest, 0, len(resources))
	for _, resource := range resources {
		typ := strings.ToLower(resource.Type)
		if typ != "qemu" && typ != "lxc" {
			continue
		}
		if resource.Node == "" || resource.VMID <= 0 {
			log.Printf("failed to read %s/%d config: resource has no node or vmid", typ, resource.VMID)
			continue
		}

		var rawConfig map[string]json.RawMessage
		path := fmt.Sprintf("/nodes/%s/%s/%d/config", url.PathEscape(resource.Node), typ, resource.VMID)
		if err := c.get(ctx, path, &rawConfig); err != nil {
			log.Printf("failed to read %s/%d config: %v", typ, resource.VMID, err)
			continue
		}

		guest := Guest{
			VMID:   resource.VMID,
			Type:   typ,
			Node:   resource.Node,
			Name:   resource.Name,
			Status: resource.Status,
			MACs:   extractMACs(rawConfig),
		}
		guests = append(guests, guest)
		log.Printf("found %s %d %q", guest.Type, guest.VMID, guest.Name)
		for _, mac := range guest.MACs {
			log.Printf("  %s", mac)
		}
	}

	return guests, nil
}

func (c *Client) Start(ctx context.Context, guest Guest) error {
	typ := strings.ToLower(guest.Type)
	if typ != "qemu" && typ != "lxc" {
		return fmt.Errorf("unsupported guest type %q", guest.Type)
	}
	path := fmt.Sprintf("/nodes/%s/%s/%d/status/start", url.PathEscape(guest.Node), typ, guest.VMID)
	return c.do(ctx, http.MethodPost, path, nil)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, out)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, method, c.baseURL+"/api2/json"+path, nil)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Authorization", c.authorization)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("read API response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &APIError{Status: response.StatusCode, Message: responseError(body)}
	}
	if out == nil || len(body) == 0 {
		return nil
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode API data: %w", err)
	}
	return nil
}

func responseError(body []byte) string {
	var envelope struct {
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Message != "" {
			return envelope.Message
		}
		if len(envelope.Errors) > 0 {
			keys := make([]string, 0, len(envelope.Errors))
			for key := range envelope.Errors {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, key := range keys {
				parts = append(parts, key+": "+envelope.Errors[key])
			}
			return strings.Join(parts, "; ")
		}
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		return "empty response"
	}
	return message
}

func extractMACs(rawConfig map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(rawConfig))
	for key := range rawConfig {
		if netKeyPattern.MatchString(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	seen := make(map[string]struct{})
	for _, key := range keys {
		var value string
		if err := json.Unmarshal(rawConfig[key], &value); err != nil {
			continue
		}
		for _, match := range macPattern.FindAllString(value, -1) {
			mac, err := normalizeMAC(match)
			if err != nil {
				continue
			}
			seen[mac] = struct{}{}
		}
	}

	macs := make([]string, 0, len(seen))
	for mac := range seen {
		macs = append(macs, mac)
	}
	sort.Strings(macs)
	return macs
}

func normalizeMAC(value string) (string, error) {
	hardware, err := net.ParseMAC(value)
	if err != nil || len(hardware) != 6 {
		return "", fmt.Errorf("invalid MAC %q", value)
	}
	parts := make([]string, len(hardware))
	for i, octet := range hardware {
		parts[i] = strconv.FormatUint(uint64(octet), 16)
		if len(parts[i]) == 1 {
			parts[i] = "0" + parts[i]
		}
		parts[i] = strings.ToUpper(parts[i])
	}
	return strings.Join(parts, ":"), nil
}
