// Code generated from schema/runtime-v1.schema.json; DO NOT EDIT.
export type OwnerScope = "business" | "client" | "user";
export type CredentialMode = "managed" | "request_scoped" | "workload" | "none";
export type UsageSource = "missing" | "provider_reported" | "derived" | "estimated" | "unavailable";

export interface Target { provider: string; model: string; region?: string; endpoint?: string }
export interface AvailableTarget { id: string; target: Target; owner_scope?: OwnerScope; credential_mode?: CredentialMode; capabilities: string[] }
export interface AvailableTargetList {
  revision: string; binding_version?: number; generated_at: string; refresh_after?: string;
  stale_until?: string; default_target_id: string; targets: AvailableTarget[];
}
export interface TokenPair { access_token: string; refresh_token: string; access_expires_at: string; family_expires_at: string }
export interface Usage {
  source: UsageSource; input_tokens?: number; output_tokens?: number; total_tokens?: number;
  cached_read?: number; cached_write?: number; reasoning_tokens?: number;
}
export interface APIError {
  kind: string; message: string; retryable?: boolean; retry_after_ms?: number;
  status_code?: number; provider_code?: string; provider_request_id?: string;
}
export interface StreamEvent {
  type: string; request_id: string; response_id: string; sequence: number; target_id?: string;
  index?: number; text?: string; arguments_delta?: string; usage?: Usage;
  finish_reason?: string; error?: APIError;
}
