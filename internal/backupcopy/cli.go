package backupcopy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

const DefaultTerminationMessagePath = "/dev/termination-log"

// TerminationPayload is written to the container termination log on completion.
type TerminationPayload struct {
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	ObjectCount int    `json:"objectCount,omitempty"`
	Error       string `json:"error,omitempty"`
}

// IsBackupWorkerCommand reports whether args invokes one of the backup worker subcommands.
func IsBackupWorkerCommand(args []string) bool {
	return len(args) > 0 && (args[0] == "backup-copy" || args[0] == "backup-delete")
}

// RunCommand executes the backup-copy or backup-delete subcommand.
func RunCommand(ctx context.Context, args []string, newStore func(bucket string) ObjectStore, termPath string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing subcommand")
	}
	if newStore == nil {
		newStore = func(bucket string) ObjectStore { return NewGCSObjectStore(bucket) }
	}
	if termPath == "" {
		termPath = DefaultTerminationMessagePath
	}

	var err error
	var payload TerminationPayload
	switch args[0] {
	case "backup-copy":
		payload, err = runCopyCommand(ctx, args[1:], newStore)
	case "backup-delete":
		err = runDeleteCommand(ctx, args[1:], newStore)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}

	if err != nil {
		payload.Error = err.Error()
		_ = writeTerminationMessage(termPath, payload)
		return err
	}
	_ = writeTerminationMessage(termPath, payload)
	return nil
}

func runCopyCommand(ctx context.Context, args []string, newStore func(bucket string) ObjectStore) (TerminationPayload, error) {
	fs := pflag.NewFlagSet("backup-copy", pflag.ContinueOnError)
	var srcBucket, dstBucket string
	var extensionsCSV string
	var req CopyRequest
	fs.StringVar(&srcBucket, "source-bucket", "", "source Barman bucket name")
	fs.StringVar(&dstBucket, "dr-bucket", "", "destination DR bucket name")
	fs.StringVar(&req.BackupName, "backup-name", "", "backup name (isolated prefix in DR bucket)")
	fs.StringVar(&req.ServerName, "server-name", "", "Barman server name")
	fs.StringVar(&req.BackupID, "backup-id", "", "Barman backup ID")
	fs.StringVar(&req.BeginWal, "begin-wal", "", "starting WAL segment")
	fs.StringVar(&req.EndWal, "end-wal", "", "ending WAL segment")
	fs.StringVar(&req.SourceUID, "source-uid", "", "source Postgres UID")
	fs.StringVar(&req.MajorVersion, "major-version", "", "source PostgreSQL major version")
	fs.StringVar(&extensionsCSV, "extensions", "", "comma-separated source extensions")
	fs.StringVar(&req.DiskSize, "disk-size", "", "source disk size")
	if err := fs.Parse(args); err != nil {
		return TerminationPayload{}, err
	}
	if srcBucket == "" || dstBucket == "" {
		return TerminationPayload{}, fmt.Errorf("--source-bucket and --dr-bucket are required")
	}
	if extensionsCSV != "" {
		req.Extensions = strings.Split(extensionsCSV, ",")
	}
	res, err := CopyBackup(ctx, newStore(srcBucket), newStore(dstBucket), req)
	if err != nil {
		return TerminationPayload{}, err
	}
	return TerminationPayload{
		SizeBytes:   res.SizeBytes,
		ObjectCount: res.ObjectCount,
	}, nil
}

func runDeleteCommand(ctx context.Context, args []string, newStore func(bucket string) ObjectStore) error {
	fs := pflag.NewFlagSet("backup-delete", pflag.ContinueOnError)
	var dstBucket, backupName string
	fs.StringVar(&dstBucket, "dr-bucket", "", "destination DR bucket name")
	fs.StringVar(&backupName, "backup-name", "", "backup name (isolated prefix in DR bucket)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if dstBucket == "" || backupName == "" {
		return fmt.Errorf("--dr-bucket and --backup-name are required")
	}
	return DeleteBackup(ctx, newStore(dstBucket), backupName)
}

func writeTerminationMessage(path string, payload TerminationPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
