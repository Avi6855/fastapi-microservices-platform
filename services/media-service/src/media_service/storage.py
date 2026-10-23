from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Protocol
from urllib.parse import quote

import boto3  # type: ignore[import-untyped]
from azure.core.exceptions import AzureError, ResourceExistsError, ResourceNotFoundError
from azure.storage.blob import (
    BlobSasPermissions,
    BlobServiceClient,
    ContainerClient,
    ContentSettings,
    generate_blob_sas,
)
from botocore.client import Config  # type: ignore[import-untyped]
from botocore.exceptions import BotoCoreError  # type: ignore[import-untyped]

from media_service.config import Settings

# Transient provider failures that callers translate into retries. Both adapters
# share one tuple so background tasks do not need to know which provider is active.
STORAGE_TRANSIENT_ERRORS: tuple[type[Exception], ...] = (
    BotoCoreError,
    AzureError,
    ConnectionError,
)

SAS_CLOCK_SKEW = timedelta(minutes=5)


@dataclass(frozen=True)
class ObjectHead:
    content_type: str
    size_bytes: int


class ObjectStorage(Protocol):
    def ensure_bucket(self) -> None: ...
    def create_upload_url(self, *, object_key: str, content_type: str, expires_in: int) -> str: ...
    def create_download_url(self, *, object_key: str, expires_in: int) -> str: ...
    def head(self, *, object_key: str) -> ObjectHead: ...
    def get_bytes(self, *, object_key: str) -> bytes: ...
    def put_bytes(self, *, object_key: str, content_type: str, data: bytes) -> None: ...
    def delete_objects(self, *, object_keys: Sequence[str]) -> None: ...


class S3ObjectStorage:
    def __init__(self, settings: Settings) -> None:
        client_options = {
            "aws_access_key_id": settings.s3_access_key_id,
            "aws_secret_access_key": settings.s3_secret_access_key,
            "region_name": settings.s3_region,
            "config": Config(signature_version="s3v4", s3={"addressing_style": "path"}),
        }
        self._bucket = settings.s3_bucket
        self._internal = boto3.client("s3", endpoint_url=settings.s3_endpoint_url, **client_options)
        self._public = boto3.client(
            "s3", endpoint_url=settings.s3_public_endpoint_url, **client_options
        )

    def ensure_bucket(self) -> None:
        existing = {bucket["Name"] for bucket in self._internal.list_buckets()["Buckets"]}
        if self._bucket not in existing:
            self._internal.create_bucket(Bucket=self._bucket)

    def create_upload_url(self, *, object_key: str, content_type: str, expires_in: int) -> str:
        return str(
            self._public.generate_presigned_url(
                "put_object",
                Params={"Bucket": self._bucket, "Key": object_key, "ContentType": content_type},
                ExpiresIn=expires_in,
                HttpMethod="PUT",
            )
        )

    def create_download_url(self, *, object_key: str, expires_in: int) -> str:
        return str(
            self._public.generate_presigned_url(
                "get_object",
                Params={"Bucket": self._bucket, "Key": object_key},
                ExpiresIn=expires_in,
            )
        )

    def head(self, *, object_key: str) -> ObjectHead:
        result = self._internal.head_object(Bucket=self._bucket, Key=object_key)
        return ObjectHead(
            content_type=str(result.get("ContentType", "")),
            size_bytes=int(result["ContentLength"]),
        )

    def get_bytes(self, *, object_key: str) -> bytes:
        result = self._internal.get_object(Bucket=self._bucket, Key=object_key)
        return bytes(result["Body"].read())

    def put_bytes(self, *, object_key: str, content_type: str, data: bytes) -> None:
        self._internal.put_object(
            Bucket=self._bucket, Key=object_key, Body=data, ContentType=content_type
        )

    def delete_objects(self, *, object_keys: Sequence[str]) -> None:
        for object_key in object_keys:
            self._internal.delete_object(Bucket=self._bucket, Key=object_key)


class AzureBlobObjectStorage:
    """Azure Blob Storage adapter for the S3-compatible `ObjectStorage` port.

    Containers map to buckets and blobs map to objects. Direct client transfer uses
    a short-lived service SAS signed with the account key, because that is the one
    credential form Azurite and a plain storage account share. Managed-identity
    signing needs a user-delegation key and is deliberately out of scope here.
    """

    def __init__(self, settings: Settings) -> None:
        missing = [
            name
            for name, value in (
                ("MEDIA_AZURE_STORAGE_ACCOUNT_URL", settings.azure_storage_account_url),
                ("MEDIA_AZURE_STORAGE_ACCOUNT_NAME", settings.azure_storage_account_name),
                ("MEDIA_AZURE_STORAGE_ACCOUNT_KEY", settings.azure_storage_account_key),
            )
            if not value
        ]
        if missing:
            raise ValueError("object storage provider azure requires " + ", ".join(sorted(missing)))
        self._account_name = settings.azure_storage_account_name
        self._account_key = settings.azure_storage_account_key
        self._container_name = settings.azure_storage_container
        self._account_url = settings.azure_storage_account_url.rstrip("/")
        self._public_endpoint = (
            settings.azure_storage_public_endpoint_url or self._account_url
        ).rstrip("/")
        self._service = BlobServiceClient(
            account_url=self._account_url,
            credential={
                "account_name": self._account_name,
                "account_key": self._account_key,
            },
        )

    def _container(self) -> ContainerClient:
        return self._service.get_container_client(self._container_name)

    def _sas_token(
        self,
        *,
        object_key: str,
        expires_in: int,
        permissions: BlobSasPermissions,
        content_type: str | None = None,
    ) -> str:
        now = datetime.now(UTC)
        return generate_blob_sas(
            account_name=self._account_name,
            container_name=self._container_name,
            blob_name=object_key,
            account_key=self._account_key,
            permission=permissions,
            expiry=now + timedelta(seconds=expires_in),
            start=now - SAS_CLOCK_SKEW,
            # `None` omits the `rsct` response-header override.
            content_type=content_type,
        )

    def _public_url(self, *, object_key: str, sas_token: str) -> str:
        return (
            f"{self._public_endpoint}/{self._container_name}/"
            f"{quote(object_key, safe='/')}?{sas_token}"
        )

    def ensure_bucket(self) -> None:
        try:
            self._container().create_container()
        except ResourceExistsError:
            return

    def create_upload_url(self, *, object_key: str, content_type: str, expires_in: int) -> str:
        # The SAS pins the blob content type (`rsct`) so a browser does not have to
        # send the Azure-specific `x-ms-blob-content-type` header on the PUT.
        return self._public_url(
            object_key=object_key,
            sas_token=self._sas_token(
                object_key=object_key,
                expires_in=expires_in,
                permissions=BlobSasPermissions(create=True, write=True),
                content_type=content_type,
            ),
        )

    def create_download_url(self, *, object_key: str, expires_in: int) -> str:
        return self._public_url(
            object_key=object_key,
            sas_token=self._sas_token(
                object_key=object_key,
                expires_in=expires_in,
                permissions=BlobSasPermissions(read=True),
            ),
        )

    def head(self, *, object_key: str) -> ObjectHead:
        properties = self._container().get_blob_client(object_key).get_blob_properties()
        content_settings = properties.content_settings
        content_type = content_settings.content_type if content_settings is not None else None
        return ObjectHead(content_type=content_type or "", size_bytes=int(properties.size or 0))

    def get_bytes(self, *, object_key: str) -> bytes:
        return bytes(self._container().get_blob_client(object_key).download_blob().readall())

    def put_bytes(self, *, object_key: str, content_type: str, data: bytes) -> None:
        self._container().upload_blob(
            name=object_key,
            data=data,
            overwrite=True,
            content_settings=ContentSettings(content_type=content_type),
        )

    def delete_objects(self, *, object_keys: Sequence[str]) -> None:
        for object_key in object_keys:
            try:
                self._container().delete_blob(object_key)
            except ResourceNotFoundError:
                continue


def build_object_storage(settings: Settings) -> ObjectStorage:
    if settings.object_storage_provider == "azure":
        return AzureBlobObjectStorage(settings)
    return S3ObjectStorage(settings)
