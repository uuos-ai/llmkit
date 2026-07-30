"""Code generated from schema/runtime-v1.schema.json; DO NOT EDIT."""
from typing import Literal, NotRequired, TypedDict

OwnerScope = Literal["business", "client", "user"]
CredentialMode = Literal["managed", "request_scoped", "workload", "none"]
UsageSource = Literal["missing", "provider_reported", "derived", "estimated", "unavailable"]

class Target(TypedDict):
    provider: str
    model: str
    region: NotRequired[str]
    endpoint: NotRequired[str]

class AvailableTarget(TypedDict):
    id: str
    target: Target
    owner_scope: NotRequired[OwnerScope]
    credential_mode: NotRequired[CredentialMode]
    capabilities: list[str]

class AvailableTargetList(TypedDict):
    revision: str
    binding_version: NotRequired[int]
    generated_at: str
    refresh_after: NotRequired[str]
    stale_until: NotRequired[str]
    default_target_id: str
    targets: list[AvailableTarget]

class TokenPair(TypedDict):
    access_token: str
    refresh_token: str
    access_expires_at: str
    family_expires_at: str

class Usage(TypedDict):
    source: UsageSource
    input_tokens: NotRequired[int]
    output_tokens: NotRequired[int]
    total_tokens: NotRequired[int]
    cached_read: NotRequired[int]
    cached_write: NotRequired[int]
    reasoning_tokens: NotRequired[int]

class APIError(TypedDict):
    kind: str
    message: str
    retryable: NotRequired[bool]
    retry_after_ms: NotRequired[int]
    status_code: NotRequired[int]
    provider_code: NotRequired[str]
    provider_request_id: NotRequired[str]

class StreamEvent(TypedDict):
    type: str
    request_id: str
    response_id: str
    sequence: int
    target_id: NotRequired[str]
    index: NotRequired[int]
    text: NotRequired[str]
    arguments_delta: NotRequired[str]
    usage: NotRequired[Usage]
    finish_reason: NotRequired[str]
    error: NotRequired[APIError]
