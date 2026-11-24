package backupcopy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/backupcopy"
)

type memoryObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemoryObjectStore(initial map[string]string) *memoryObjectStore {
	objs := make(map[string][]byte, len(initial))
	for k, v := range initial {
		objs[k] = []byte(v)
	}
	return &memoryObjectStore{objects: objs}
}

func (m *memoryObjectStore) List(_ context.Context, prefix string) ([]backupcopy.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []backupcopy.ObjectInfo
	for k, data := range m.objects {
		if strings.HasPrefix(k, prefix) {
			sum := sha256.Sum256(data)
			res = append(res, backupcopy.ObjectInfo{
				Key:    k,
				Size:   int64(len(data)),
				SHA256: hex.EncodeToString(sum[:]),
			})
		}
	}
	slices.SortFunc(res, func(a, b backupcopy.ObjectInfo) int { return strings.Compare(a.Key, b.Key) })
	return res, nil
}

func (m *memoryObjectStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %q not found", key)
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}

func (m *memoryObjectStore) Put(_ context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memoryObjectStore) Stat(_ context.Context, key string) (backupcopy.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return backupcopy.ObjectInfo{}, fmt.Errorf("object %q not found", key)
	}
	sum := sha256.Sum256(data)
	return backupcopy.ObjectInfo{
		Key:    key,
		Size:   int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func (m *memoryObjectStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func TestCopyBackupCopiesOnlyTargetBackupAndRequiredWALRange(t *testing.T) {
	src := newMemoryObjectStore(map[string]string{
		"pg-orders-main-65648812/base/20261123T020000/backup.info":                   "old-backup-info",
		"pg-orders-main-65648812/base/20261124T020000/backup.info":                   "target-backup-info",
		"pg-orders-main-65648812/base/20261124T020000/data.tar.lz4":                  "target-base-data-payload",
		"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000001.lz4": "wal-before-range",
		"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000002.lz4": "wal-begin",
		"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000003.lz4": "wal-middle",
		"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000004.lz4": "wal-end",
		"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000005.lz4": "wal-after-range",
		"pg-orders-main-65648812/wals/00000002.history.lz4":                          "timeline-history",
	})
	dst := newMemoryObjectStore(nil)

	fixedTime := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	res, err := backupcopy.CopyBackup(context.Background(), src, dst, backupcopy.CopyRequest{
		BackupName:   "orders-backup-1",
		ServerName:   "pg-orders-main-65648812",
		BackupID:     "20261124T020000",
		BeginWal:     "000000010000000000000002",
		EndWal:       "000000010000000000000004",
		SourceUID:    "pg-uid-1234",
		MajorVersion: "18",
		Extensions:   []string{"postgis"},
		DiskSize:     "20Gi",
		Now:          func() time.Time { return fixedTime },
	})
	if err != nil {
		t.Fatalf("CopyBackup() error = %v", err)
	}
	if res.ObjectCount != 6 {
		t.Errorf("ObjectCount = %d, want 6 (2 base + 3 WAL + 1 history)", res.ObjectCount)
	}

	copied, err := dst.List(context.Background(), "orders-backup-1/")
	if err != nil {
		t.Fatal(err)
	}
	gotKeys := make([]string, 0, len(copied))
	for _, o := range copied {
		gotKeys = append(gotKeys, o.Key)
	}
	wantKeys := []string{
		"orders-backup-1/nais-backup.json",
		"orders-backup-1/pg-orders-main-65648812/base/20261124T020000/backup.info",
		"orders-backup-1/pg-orders-main-65648812/base/20261124T020000/data.tar.lz4",
		"orders-backup-1/pg-orders-main-65648812/wals/0000000100000000/000000010000000000000002.lz4",
		"orders-backup-1/pg-orders-main-65648812/wals/0000000100000000/000000010000000000000003.lz4",
		"orders-backup-1/pg-orders-main-65648812/wals/0000000100000000/000000010000000000000004.lz4",
		"orders-backup-1/pg-orders-main-65648812/wals/00000002.history.lz4",
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("copied keys =\n  %v\nwant\n  %v", gotKeys, wantKeys)
	}

	if err := backupcopy.DeleteBackup(context.Background(), dst, "orders-backup-1"); err != nil {
		t.Fatalf("DeleteBackup() error = %v", err)
	}
	afterDelete, err := dst.List(context.Background(), "orders-backup-1/")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDelete) != 0 {
		t.Errorf("remaining objects after DeleteBackup = %v, want empty", afterDelete)
	}
}

func TestCopyBackupFailsWhenWALOrBaseIsMissing(t *testing.T) {
	t.Run("missing base backup", func(t *testing.T) {
		src := newMemoryObjectStore(nil)
		dst := newMemoryObjectStore(nil)
		_, err := backupcopy.CopyBackup(context.Background(), src, dst, backupcopy.CopyRequest{
			BackupName: "orders-backup-1",
			ServerName: "pg-orders-main-65648812",
			BackupID:   "20261124T020000",
			BeginWal:   "000000010000000000000002",
			EndWal:     "000000010000000000000002",
		})
		if err == nil || !strings.Contains(err.Error(), "contains no objects") {
			t.Errorf("err = %v, want missing base backup error", err)
		}
	})

	t.Run("missing endWal segment", func(t *testing.T) {
		src := newMemoryObjectStore(map[string]string{
			"pg-orders-main-65648812/base/20261124T020000/backup.info":                   "info",
			"pg-orders-main-65648812/wals/0000000100000000/000000010000000000000002.lz4": "wal-begin",
		})
		dst := newMemoryObjectStore(nil)
		_, err := backupcopy.CopyBackup(context.Background(), src, dst, backupcopy.CopyRequest{
			BackupName: "orders-backup-1",
			ServerName: "pg-orders-main-65648812",
			BackupID:   "20261124T020000",
			BeginWal:   "000000010000000000000002",
			EndWal:     "000000010000000000000003",
		})
		if err == nil || !strings.Contains(err.Error(), "missing required endWal") {
			t.Errorf("err = %v, want missing endWal error", err)
		}
	})
}

func TestHTTPObjectStoreRoundTrip(t *testing.T) {
	mem := newMemoryObjectStore(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/dr-bucket/o"):
			name := r.URL.Query().Get("name")
			if err := mem.Put(r.Context(), name, r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/storage/v1/b/dr-bucket/o":
			prefix := r.URL.Query().Get("prefix")
			objs, _ := mem.List(r.Context(), prefix)
			type item struct {
				Name string `json:"name"`
				Size string `json:"size"`
			}
			var resp struct {
				Items []item `json:"items"`
			}
			for _, o := range objs {
				resp.Items = append(resp.Items, item{Name: o.Key, Size: strconv.FormatInt(o.Size, 10)})
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(r.URL.Path, "/storage/v1/b/dr-bucket/o/"):
			escaped := strings.TrimPrefix(r.URL.Path, "/storage/v1/b/dr-bucket/o/")
			key, _ := url.PathUnescape(escaped)
			if r.Method == http.MethodDelete {
				_ = mem.Delete(r.Context(), key)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.Query().Get("alt") == "media" {
				rc, err := mem.Open(r.Context(), key)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer func() { _ = rc.Close() }()
				_, _ = io.Copy(w, rc)
				return
			}
			info, err := mem.Stat(r.Context(), key)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"name": info.Key,
				"size": strconv.FormatInt(info.Size, 10),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := &backupcopy.HTTPObjectStore{
		Bucket:      "dr-bucket",
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	}

	ctx := context.Background()
	if err := store.Put(ctx, "backup-1/file.txt", strings.NewReader("hello world")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	stat, err := store.Stat(ctx, "backup-1/file.txt")
	if err != nil || stat.Size != 11 {
		t.Fatalf("Stat = %+v, err = %v", stat, err)
	}
	rc, err := store.Open(ctx, "backup-1/file.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "hello world" {
		t.Errorf("Open content = %q, want %q", data, "hello world")
	}
	list, err := store.List(ctx, "backup-1/")
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, err = %v", list, err)
	}
	if err := store.Delete(ctx, "backup-1/file.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
