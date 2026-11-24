package backupcopy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGCSBaseURL     = "https://storage.googleapis.com"
	defaultMetadataTokURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
)

// TokenSource returns a bearer token for object storage HTTP requests.
type TokenSource func(ctx context.Context) (string, error)

// HTTPObjectStore implements ObjectStore over HTTP (Google Cloud Storage JSON
// API by default, with configurable base URL and token source for testing or
// alternative endpoints).
type HTTPObjectStore struct {
	Bucket      string
	BaseURL     string
	HTTPClient  *http.Client
	TokenSource TokenSource
}

func NewGCSObjectStore(bucket string) *HTTPObjectStore {
	return &HTTPObjectStore{
		Bucket:      bucket,
		BaseURL:     defaultGCSBaseURL,
		HTTPClient:  &http.Client{Timeout: 5 * time.Minute},
		TokenSource: GKEWorkloadIdentityTokenSource,
	}
}

// GKEWorkloadIdentityTokenSource fetches an OAuth2 access token from the GKE
// metadata server (or GOOGLE_OAUTH_ACCESS_TOKEN if set).
func GKEWorkloadIdentityTokenSource(ctx context.Context) (string, error) {
	if tok := os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"); tok != "" {
		return tok, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultMetadataTokURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching GKE Workload Identity token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("metadata server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decoding metadata token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("metadata server returned empty access_token")
	}
	return payload.AccessToken, nil
}

func (s *HTTPObjectStore) baseURL() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return defaultGCSBaseURL
}

func (s *HTTPObjectStore) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

func (s *HTTPObjectStore) authorizedRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if s.TokenSource != nil {
		token, err := s.TokenSource(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (s *HTTPObjectStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var result []ObjectInfo
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("prefix", prefix)
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		u := fmt.Sprintf("%s/storage/v1/b/%s/o?%s", s.baseURL(), url.PathEscape(s.Bucket), q.Encode())
		req, err := s.authorizedRequest(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client().Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("listing bucket %q prefix %q: status %d: %s", s.Bucket, prefix, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var page struct {
			Items []struct {
				Name string `json:"name"`
				Size string `json:"size"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding object list for bucket %q: %w", s.Bucket, err)
		}
		for _, item := range page.Items {
			size, err := strconv.ParseInt(item.Size, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing size %q for object %q: %w", item.Size, item.Name, err)
			}
			result = append(result, ObjectInfo{Key: item.Name, Size: size})
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return result, nil
}

func (s *HTTPObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", s.baseURL(), url.PathEscape(s.Bucket), url.PathEscape(key))
	req, err := s.authorizedRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("opening object %q in bucket %q: status %d: %s", key, s.Bucket, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func (s *HTTPObjectStore) Put(ctx context.Context, key string, r io.Reader) error {
	q := url.Values{}
	q.Set("uploadType", "media")
	q.Set("name", key)
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?%s", s.baseURL(), url.PathEscape(s.Bucket), q.Encode())
	req, err := s.authorizedRequest(ctx, http.MethodPost, u, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("uploading object %q to bucket %q: status %d: %s", key, s.Bucket, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *HTTPObjectStore) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", s.baseURL(), url.PathEscape(s.Bucket), url.PathEscape(key))
	req, err := s.authorizedRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ObjectInfo{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ObjectInfo{}, fmt.Errorf("stat object %q in bucket %q: status %d: %s", key, s.Bucket, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var item struct {
		Name string `json:"name"`
		Size string `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return ObjectInfo{}, fmt.Errorf("decoding stat response for %q: %w", key, err)
	}
	size, err := strconv.ParseInt(item.Size, 10, 64)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("parsing size %q for %q: %w", item.Size, key, err)
	}
	return ObjectInfo{Key: item.Name, Size: size}, nil
}

func (s *HTTPObjectStore) Delete(ctx context.Context, key string) error {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", s.baseURL(), url.PathEscape(s.Bucket), url.PathEscape(key))
	req, err := s.authorizedRequest(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("deleting object %q from bucket %q: status %d: %s", key, s.Bucket, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
