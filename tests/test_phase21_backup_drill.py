"""Invariants for the object-storage backup drill.

These protect the properties the ADR claims: verified copies, a refusal path for
corrupt snapshots, a portable store port, and a CI gate that builds the tool.
"""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TOOL = ROOT / "tools" / "backupctl"
WORKFLOW = ROOT / ".github" / "workflows" / "platform-ci.yml"


def _source(name: str) -> str:
    return (TOOL / name).read_text(encoding="utf-8")


def test_backup_reads_every_copy_back_before_recording_it() -> None:
    source = _source("backup.go")

    # The copy is written, read back, and compared with the source checksum
    # before the manifest entry is appended.
    assert "read back backup object" in source
    assert "does not match its source" in source
    write_position = source.index("store.Put(ctx, options.BackupBucket, backupKey")
    verify_position = source.index("read back backup object")
    append_position = source.index("manifest.Entries = append")
    assert write_position < verify_position < append_position


def test_restore_refuses_corrupt_snapshots_instead_of_repairing_them() -> None:
    source = _source("backup.go")

    assert "refusing to restore it" in source
    assert "does not match the manifest" in source
    # A dry run must not write into the target bucket.
    assert "if !options.DryRun" in source


def test_manifest_format_is_versioned_and_validated() -> None:
    source = _source("manifest.go")

    assert "manifestVersion = 1" in source
    assert "unsupported manifest version" in source
    assert '"sha256"' in source
    assert "sort.Slice" in source and "UTC()" in source


def test_backup_logic_depends_on_the_store_port_only() -> None:
    backup_source = _source("backup.go")
    store_source = _source("store.go")
    adapter_source = _source("minio_store.go")

    assert "type Store interface" in store_source
    for provider_term in ("minio", "s3", "azure"):
        assert provider_term not in backup_source.lower()
    assert "minio.Client" in adapter_source
    assert "MinIOStore) EnsureBucket" in adapter_source


def test_tool_never_embeds_a_credential() -> None:
    for source in TOOL.glob("*.go"):
        text = source.read_text(encoding="utf-8")
        assert "minio-local" not in text
        assert "backupctl-local-only" not in text
        assert not re.search(r'(?i)(secret[_-]?key\s*=\s*")[^"]+"', text)


def test_go_module_pins_the_language_and_a_library_version() -> None:
    module = (TOOL / "go.mod").read_text(encoding="utf-8")

    assert "go 1.27.3" in module
    assert re.search(r"github.com/minio/minio-go/v7 v\d+\.\d+\.\d+$", module, re.MULTILINE)
    assert (TOOL / "go.sum").is_file()


def test_runtime_image_is_minimal_and_unprivileged() -> None:
    dockerfile = (TOOL / "Dockerfile").read_text(encoding="utf-8")

    assert "FROM scratch" in dockerfile
    assert "USER 10001:10001" in dockerfile
    assert "CGO_ENABLED=0" in dockerfile
    assert re.search(r"FROM golang:1\.27\.3-bookworm@sha256:[0-9a-f]{64}", dockerfile)
    assert "latest" not in dockerfile


def test_ci_builds_tests_and_vets_the_tool_with_a_pinned_toolchain() -> None:
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "Object-storage backup drill tool" in workflow
    job = workflow.split("  backup-tool:\n", maxsplit=1)[1]
    job = job.split("\n  migration-heads:", maxsplit=1)[0]
    assert "version=1.27.3" in job
    assert 'archive_name="go${version}.linux-amd64.tar.gz"' in job
    assert re.search(r'echo "[0-9a-f]{64}  \$archive" \| sha256sum --check', job)
    for command in ("gofmt", "go vet ./...", "go test ./... -count=1", "go build"):
        assert command in job


def test_build_output_and_terraform_state_stay_out_of_git() -> None:
    gitignore = (ROOT / ".gitignore").read_text(encoding="utf-8")

    assert "/tools/backupctl/backupctl" in gitignore
    assert "**/.terraform/" in gitignore
