// Command backupctl backs up, verifies, and restores an S3-compatible bucket.
//
// It exists because "we have backups" is a claim, and a claim needs a drill: every
// backup is read back and checksummed, and every restore is verified object by
// object against the manifest written with the snapshot.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const usage = `backupctl - object-storage backup, verification, and restore drill

Usage:
  backupctl backup  --source BUCKET --backup BUCKET [--prefix P] [--snapshot ID]
  backupctl verify  --backup BUCKET --snapshot ID
  backupctl restore --backup BUCKET --snapshot ID --target BUCKET [--dry-run]

Connection flags for every command:
  --endpoint HOST:PORT   S3-compatible endpoint (env BACKUPCTL_ENDPOINT)
  --access-key KEY       access key (env BACKUPCTL_ACCESS_KEY)
  --secret-key KEY       secret key (env BACKUPCTL_SECRET_KEY)
  --tls                  use HTTPS (env BACKUPCTL_SECURE=true)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "backupctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "backup":
		return runBackup(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type connection struct {
	endpoint  string
	accessKey string
	secretKey string
	secure    bool
}

func addConnectionFlags(set *flag.FlagSet) *connection {
	conn := &connection{}
	set.StringVar(&conn.endpoint, "endpoint", envOr("BACKUPCTL_ENDPOINT", ""), "S3-compatible endpoint")
	set.StringVar(&conn.accessKey, "access-key", envOr("BACKUPCTL_ACCESS_KEY", ""), "access key")
	set.StringVar(&conn.secretKey, "secret-key", envOr("BACKUPCTL_SECRET_KEY", ""), "secret key")
	set.BoolVar(
		&conn.secure,
		"tls",
		strings.EqualFold(envOr("BACKUPCTL_SECURE", "false"), "true"),
		"use HTTPS",
	)
	return conn
}

func (c *connection) store() (*MinIOStore, error) {
	return NewMinIOStore(c.endpoint, c.accessKey, c.secretKey, c.secure)
}

func runBackup(args []string) error {
	set := flag.NewFlagSet("backup", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(set.Output(), usage) }
	conn := addConnectionFlags(set)
	source := set.String("source", "", "source bucket")
	backup := set.String("backup", "", "backup bucket")
	prefix := set.String("prefix", "", "only back up keys with this prefix")
	snapshot := set.String("snapshot", "", "snapshot ID (default: UTC timestamp)")
	if err := set.Parse(args); err != nil {
		return err
	}

	store, err := conn.store()
	if err != nil {
		return err
	}
	result, err := Backup(context.Background(), store, BackupOptions{
		SourceBucket: *source,
		BackupBucket: *backup,
		Prefix:       *prefix,
		SnapshotID:   *snapshot,
		Now:          time.Now(),
	})
	if err != nil {
		return err
	}
	fmt.Printf(
		"snapshot %s: %d objects, %d bytes, manifest %s\n",
		result.Manifest.SnapshotID,
		len(result.Manifest.Entries),
		result.Manifest.TotalBytes,
		result.ManifestKey,
	)
	return nil
}

func runVerify(args []string) error {
	set := flag.NewFlagSet("verify", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(set.Output(), usage) }
	conn := addConnectionFlags(set)
	backup := set.String("backup", "", "backup bucket")
	snapshot := set.String("snapshot", "", "snapshot ID")
	if err := set.Parse(args); err != nil {
		return err
	}

	store, err := conn.store()
	if err != nil {
		return err
	}
	result, err := Verify(context.Background(), store, *backup, *snapshot)
	if err != nil {
		return err
	}
	fmt.Printf(
		"snapshot %s verified: %d objects, %d bytes\n",
		result.SnapshotID,
		result.Entries,
		result.TotalBytes,
	)
	return nil
}

func runRestore(args []string) error {
	set := flag.NewFlagSet("restore", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(set.Output(), usage) }
	conn := addConnectionFlags(set)
	backup := set.String("backup", "", "backup bucket")
	snapshot := set.String("snapshot", "", "snapshot ID")
	target := set.String("target", "", "target bucket")
	dryRun := set.Bool("dry-run", false, "verify the snapshot without writing")
	if err := set.Parse(args); err != nil {
		return err
	}

	store, err := conn.store()
	if err != nil {
		return err
	}
	result, err := Restore(context.Background(), store, RestoreOptions{
		BackupBucket: *backup,
		SnapshotID:   *snapshot,
		TargetBucket: *target,
		DryRun:       *dryRun,
	})
	if err != nil {
		return err
	}
	mode := "restored"
	if result.DryRun {
		mode = "dry run: would restore"
	}
	fmt.Printf(
		"snapshot %s %s %d objects, %d bytes\n",
		result.SnapshotID,
		mode,
		result.Restored,
		result.TotalBytes,
	)
	return nil
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok && value != "" {
		return value
	}
	return fallback
}
