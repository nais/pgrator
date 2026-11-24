// Package backupcopy implements vendor-agnostic copying, verification, and
// cleanup of standalone Postgres backups between a source Barman bucket and a
// team disaster-recovery bucket.
package backupcopy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	// ManifestFileName is written at the root of a backup's isolated prefix in
	// the DR bucket once all base and WAL objects have been copied and verified.
	ManifestFileName = "nais-backup.json"

	walSegmentNameLength = 24
)

// ObjectInfo describes one object in an ObjectStore.
type ObjectInfo struct {
	Key    string `json:"key"`
	Size   int64  `json:"sizeBytes"`
	SHA256 string `json:"sha256,omitempty"`
}

// ObjectStore is the vendor-agnostic storage interface used by the backup copy
// and cleanup worker.
type ObjectStore interface {
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, r io.Reader) error
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}

// CopyRequest specifies the source backup and metadata to copy into an
// isolated prefix in the destination DR bucket.
type CopyRequest struct {
	BackupName      string
	ServerName      string
	BackupID        string
	BeginWal        string
	EndWal          string
	SourceUID       string
	MajorVersion    string
	Extensions      []string
	DiskSize        string
	Now             func() time.Time
	WALWaitTimeout  time.Duration
	WALPollInterval time.Duration
}

// Manifest is serialized as nais-backup.json inside the backup's DR prefix.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	BackupName    string       `json:"backupName"`
	SourceUID     string       `json:"sourceUID,omitempty"`
	MajorVersion  string       `json:"majorVersion,omitempty"`
	Extensions    []string     `json:"extensions,omitempty"`
	DiskSize      string       `json:"diskSize,omitempty"`
	ServerName    string       `json:"serverName"`
	BackupID      string       `json:"backupID"`
	BeginWal      string       `json:"beginWal"`
	EndWal        string       `json:"endWal"`
	CreatedAt     time.Time    `json:"createdAt"`
	SizeBytes     int64        `json:"sizeBytes"`
	Objects       []ObjectInfo `json:"objects"`
}

// CopyResult summarizes a completed and verified backup copy.
type CopyResult struct {
	SizeBytes   int64    `json:"sizeBytes"`
	ObjectCount int      `json:"objectCount"`
	Manifest    Manifest `json:"-"`
}

// CopyBackup copies the base backup and required WAL range [BeginWal, EndWal]
// from src to dst under <BackupName>/, verifies every copied object's size and
// SHA-256 digest, and writes <BackupName>/nais-backup.json last.
func CopyBackup(ctx context.Context, src, dst ObjectStore, req CopyRequest) (CopyResult, error) {
	if err := validateCopyRequest(req); err != nil {
		return CopyResult{}, err
	}

	basePrefix := fmt.Sprintf("%s/base/%s/", req.ServerName, req.BackupID)
	baseObjects, err := src.List(ctx, basePrefix)
	if err != nil {
		return CopyResult{}, fmt.Errorf("listing source base backup %q: %w", basePrefix, err)
	}
	if len(baseObjects) == 0 {
		return CopyResult{}, fmt.Errorf("source base backup %q contains no objects (may have expired from barman retention)", basePrefix)
	}
	hasBackupInfo := slices.ContainsFunc(baseObjects, func(o ObjectInfo) bool {
		return o.Key == basePrefix+"backup.info" && o.Size > 0
	})
	if !hasBackupInfo {
		return CopyResult{}, fmt.Errorf("source base backup %q is missing backup.info", basePrefix)
	}

	metadata, err := readBarmanMetadata(ctx, src, basePrefix+"backup.info")
	if err != nil {
		return CopyResult{}, err
	}
	if err := validateBaseArchives(baseObjects, basePrefix, metadata.tablespaces); err != nil {
		return CopyResult{}, err
	}
	wal, err := newWALRange(req.BeginWal, req.EndWal, metadata.walSegmentSize)
	if err != nil {
		return CopyResult{}, err
	}
	walsPrefix := fmt.Sprintf("%s/wals/", req.ServerName)
	walObjects, err := waitForRequiredWALObjects(ctx, src, walsPrefix, wal, req.WALWaitTimeout, req.WALPollInterval)
	if err != nil {
		return CopyResult{}, err
	}

	toCopy := make([]ObjectInfo, 0, len(baseObjects)+len(walObjects))
	toCopy = append(toCopy, baseObjects...)
	toCopy = append(toCopy, walObjects...)
	slices.SortFunc(toCopy, func(a, b ObjectInfo) int { return strings.Compare(a.Key, b.Key) })

	copied := make([]ObjectInfo, 0, len(toCopy))
	var totalBytes int64
	for _, srcObj := range toCopy {
		dstKey := req.BackupName + "/" + strings.TrimPrefix(srcObj.Key, "/")
		written, digest, err := copyAndVerifyObject(ctx, src, dst, srcObj, dstKey)
		if err != nil {
			return CopyResult{}, err
		}
		totalBytes += written
		copied = append(copied, ObjectInfo{
			Key:    strings.TrimPrefix(srcObj.Key, "/"),
			Size:   written,
			SHA256: digest,
		})
	}

	nowFn := req.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	manifest := Manifest{
		SchemaVersion: 1,
		BackupName:    req.BackupName,
		SourceUID:     req.SourceUID,
		MajorVersion:  req.MajorVersion,
		Extensions:    slices.Clone(req.Extensions),
		DiskSize:      req.DiskSize,
		ServerName:    req.ServerName,
		BackupID:      req.BackupID,
		BeginWal:      req.BeginWal,
		EndWal:        req.EndWal,
		CreatedAt:     nowFn().UTC().Truncate(time.Second),
		SizeBytes:     totalBytes,
		Objects:       copied,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return CopyResult{}, fmt.Errorf("marshaling backup manifest: %w", err)
	}
	manifestKey := req.BackupName + "/" + ManifestFileName
	if err := dst.Put(ctx, manifestKey, bytes.NewReader(manifestBytes)); err != nil {
		return CopyResult{}, fmt.Errorf("writing backup manifest %q: %w", manifestKey, err)
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	if err := verifyObject(ctx, dst, manifestKey, int64(len(manifestBytes)), hex.EncodeToString(manifestDigest[:])); err != nil {
		return CopyResult{}, fmt.Errorf("verifying backup manifest %q: %w", manifestKey, err)
	}

	return CopyResult{
		SizeBytes:   totalBytes,
		ObjectCount: len(copied),
		Manifest:    manifest,
	}, nil
}

// DeleteBackup removes all objects under <backupName>/ in dst and verifies that
// no objects remain under the prefix.
func DeleteBackup(ctx context.Context, dst ObjectStore, backupName string) error {
	trimmed := strings.Trim(backupName, "/")
	if trimmed == "" {
		return fmt.Errorf("backupName must not be empty")
	}
	prefix := trimmed + "/"
	objects, err := dst.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("listing objects under %q for deletion: %w", prefix, err)
	}
	for _, obj := range objects {
		if err := dst.Delete(ctx, obj.Key); err != nil {
			return fmt.Errorf("deleting object %q: %w", obj.Key, err)
		}
	}
	remaining, err := dst.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("verifying deletion of prefix %q: %w", prefix, err)
	}
	if len(remaining) > 0 {
		return fmt.Errorf("prefix %q still has %d objects after deletion", prefix, len(remaining))
	}
	return nil
}

func validateCopyRequest(req CopyRequest) error {
	if strings.Trim(req.BackupName, "/") == "" {
		return fmt.Errorf("backupName is required")
	}
	if req.ServerName == "" {
		return fmt.Errorf("serverName is required")
	}
	if req.BackupID == "" {
		return fmt.Errorf("backupID is required")
	}
	if !isWALSegmentName(req.BeginWal) || !isWALSegmentName(req.EndWal) {
		return fmt.Errorf("beginWal (%q) and endWal (%q) must be 24-character hex WAL segment names", req.BeginWal, req.EndWal)
	}
	return nil
}

func waitForRequiredWALObjects(
	ctx context.Context,
	src ObjectStore,
	walsPrefix string,
	wal walRange,
	waitTimeout, pollInterval time.Duration,
) ([]ObjectInfo, error) {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if waitTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, waitTimeout)
		defer cancel()
	}
	var missing error
	for {
		allWalObjects, err := src.List(ctx, walsPrefix)
		if err != nil {
			if missing != nil && ctx.Err() != nil {
				return nil, fmt.Errorf("waiting for WAL: %w (%w)", missing, ctx.Err())
			}
			return nil, fmt.Errorf("listing source WAL archive %q: %w", walsPrefix, err)
		}
		walObjects, err := wal.selectObjects(walsPrefix, allWalObjects)
		if err == nil {
			return walObjects, nil
		}
		if waitTimeout <= 0 {
			return nil, err
		}
		missing = err
		if err := sleepWithContext(ctx, pollInterval); err != nil {
			return nil, fmt.Errorf("waiting for WAL: %w (%w)", missing, err)
		}
	}
}

func stripCompressionExt(name string) string {
	for _, ext := range []string{".lz4", ".gz", ".zst", ".bz2", ".xz", ".snappy"} {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext)
		}
	}
	return name
}

func isWALSegmentName(s string) bool {
	if len(s) != walSegmentNameLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func copyAndVerifyObject(ctx context.Context, src, dst ObjectStore, srcObj ObjectInfo, dstKey string) (int64, string, error) {
	rc, err := src.Open(ctx, srcObj.Key)
	if err != nil {
		return 0, "", fmt.Errorf("opening source object %q: %w", srcObj.Key, err)
	}
	defer func() { _ = rc.Close() }()

	hasher := sha256.New()
	counter := &countingReader{r: io.TeeReader(rc, hasher)}

	if err := dst.Put(ctx, dstKey, counter); err != nil {
		return 0, "", fmt.Errorf("writing destination object %q: %w", dstKey, err)
	}
	if counter.n != srcObj.Size {
		return 0, "", fmt.Errorf("object %q streamed %d bytes, expected %d", srcObj.Key, counter.n, srcObj.Size)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyObject(ctx, dst, dstKey, counter.n, digest); err != nil {
		return 0, "", fmt.Errorf("verifying destination object %q: %w", dstKey, err)
	}
	return counter.n, digest, nil
}

func verifyObject(ctx context.Context, dst ObjectStore, key string, size int64, digest string) error {
	stat, err := dst.Stat(ctx, key)
	if err != nil {
		return err
	}
	if stat.Size != size {
		return fmt.Errorf("size = %d, want %d", stat.Size, size)
	}
	actual := stat.SHA256
	if actual == "" {
		rc, err := dst.Open(ctx, key)
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		h := sha256.New()
		n, err := io.Copy(h, rc)
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("read back %d bytes, want %d", n, size)
		}
		actual = hex.EncodeToString(h.Sum(nil))
	}
	if actual != digest {
		return fmt.Errorf("sha256 = %s, want %s", actual, digest)
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
