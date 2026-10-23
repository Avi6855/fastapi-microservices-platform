"""Azure Blob adapter compatibility tests.

The selection tests always run. The provider-compatibility suite needs a Blob
endpoint and is opt-in, like the Compose E2E suite: set
``MEDIA_AZURE_STORAGE_LIVE=1`` and optionally override
``MEDIA_AZURE_STORAGE_ACCOUNT_URL``, ``MEDIA_AZURE_STORAGE_ACCOUNT_NAME`` and
``MEDIA_AZURE_STORAGE_ACCOUNT_KEY``. The defaults target the opt-in
``azure-blob`` Compose profile (or a local Azurite process on port 10000).
"""

import os
from uuid import uuid4

import httpx
import pytest
from azure.core.exceptions import ResourceNotFoundError

from media_service.config import Settings
from media_service.storage import (
    AzureBlobObjectStorage,
    ObjectStorage,
    S3ObjectStorage,
    build_object_storage,
)

LIVE = os.environ.get("MEDIA_AZURE_STORAGE_LIVE") == "1"

AZURITE_ACCOUNT_URL = "http://127.0.0.1:10000/devstoreaccount1"
AZURITE_ACCOUNT_NAME = "devstoreaccount1"
AZURITE_ACCOUNT_KEY = (
    "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

requires_live_blob_endpoint = pytest.mark.skipif(
    not LIVE, reason="set MEDIA_AZURE_STORAGE_LIVE=1 with a reachable Blob endpoint"
)


def _azure_settings(**overrides: str) -> Settings:
    values: dict[str, str] = {
        "object_storage_provider": "azure",
        "azure_storage_account_url": os.environ.get(
            "MEDIA_AZURE_STORAGE_ACCOUNT_URL", AZURITE_ACCOUNT_URL
        ),
        "azure_storage_account_name": os.environ.get(
            "MEDIA_AZURE_STORAGE_ACCOUNT_NAME", AZURITE_ACCOUNT_NAME
        ),
        "azure_storage_account_key": os.environ.get(
            "MEDIA_AZURE_STORAGE_ACCOUNT_KEY", AZURITE_ACCOUNT_KEY
        ),
        "azure_storage_public_endpoint_url": os.environ.get(
            "MEDIA_AZURE_STORAGE_ACCOUNT_URL", AZURITE_ACCOUNT_URL
        ),
    }
    values.update(overrides)
    return Settings(**values)


def test_default_provider_remains_s3() -> None:
    storage = build_object_storage(Settings())

    assert isinstance(storage, S3ObjectStorage)


def test_azure_provider_selects_the_blob_adapter() -> None:
    storage: ObjectStorage = build_object_storage(_azure_settings())

    assert isinstance(storage, AzureBlobObjectStorage)


def test_azure_provider_rejects_incomplete_configuration() -> None:
    with pytest.raises(ValueError, match="MEDIA_AZURE_STORAGE_ACCOUNT_KEY"):
        AzureBlobObjectStorage(_azure_settings(azure_storage_account_key=""))


@requires_live_blob_endpoint
def test_azure_blob_round_trip_matches_the_object_storage_port() -> None:
    container = f"media-compat-{uuid4().hex[:12]}"
    storage = AzureBlobObjectStorage(_azure_settings(azure_storage_container=container))
    object_key = "original/customer/asset.bin"
    payload = b"provider-compatibility-bytes"

    storage.ensure_bucket()
    storage.ensure_bucket()  # idempotent: the container already exists
    try:
        storage.put_bytes(object_key=object_key, content_type="image/webp", data=payload)

        head = storage.head(object_key=object_key)
        assert head.content_type == "image/webp"
        assert head.size_bytes == len(payload)
        assert storage.get_bytes(object_key=object_key) == payload

        storage.delete_objects(object_keys=[object_key, object_key])
        with pytest.raises(ResourceNotFoundError):
            storage.head(object_key=object_key)
    finally:
        storage._service.delete_container(container)  # noqa: SLF001 - test cleanup


@requires_live_blob_endpoint
def test_azure_presigned_urls_transfer_bytes_directly() -> None:
    container = f"media-compat-{uuid4().hex[:12]}"
    storage = AzureBlobObjectStorage(_azure_settings(azure_storage_container=container))
    upload_key = "derivatives/customer/asset-thumbnail.webp"
    payload = b"direct-client-transfer-bytes"

    storage.ensure_bucket()
    try:
        # A direct uploader must declare the blob type, and Azure stores the content
        # type from `x-ms-blob-content-type` rather than the request `Content-Type`.
        # The SAS additionally pins the download `Content-Type` (`rsct`).
        with httpx.Client(timeout=10.0) as client:
            upload_response = client.put(
                storage.create_upload_url(
                    object_key=upload_key, content_type="image/webp", expires_in=120
                ),
                content=payload,
                headers={"x-ms-blob-type": "BlockBlob", "x-ms-blob-content-type": "image/webp"},
            )
        assert upload_response.status_code == 201

        assert storage.head(object_key=upload_key).content_type == "image/webp"

        with httpx.Client(timeout=10.0) as client:
            download_response = client.get(
                storage.create_download_url(object_key=upload_key, expires_in=120)
            )
        assert download_response.status_code == 200
        assert download_response.content == payload
        assert download_response.headers["content-type"] == "image/webp"
    finally:
        storage._service.delete_container(container)  # noqa: SLF001 - test cleanup
