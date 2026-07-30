# Implementation Plan: llmkit 通用 Provider SDK 与三模式运行时

**Branch**: `dev` | **Date**: 2026-07-30 | **Spec**: [spec.md](./spec.md)

## Summary

以 Go adapter core 为唯一 Provider 实现，通过 IPC/HTTPS 暴露三模式；用版本化身份、目标快照、凭据租约、规范流/错误/usage 和 conformance suite 约束所有实现。

## Technical Context

**Language/Version**: Go 1.24；P0 Rust client  
**Primary Dependencies**: Go 标准库、go-winio、yaml.v3、modernc SQLite、OS keyring  
**Storage**: sidecar 无状态；local 可选 SQLite+Keychain；gateway 外部 ports  
**Testing**: go test、httptest/mock upstream、race、cross compile、GitHub Actions  
**Target Platform**: Linux/macOS/Windows，本机 IPC 与 HTTPS gateway  
**Project Type**: library + optional daemon  
**Constraints**: 不持久化正文；无 secret 泄露；明确有界 retry/fallback；Apache-2.0 clean-room

## Constitution Check

- Library first: PASS
- Protocol correctness and conformance: PASS
- Isolation/secret safety: PASS
- Deterministic reliability: PASS
- No AGPL code reuse: PASS

## Project Structure

```text
identity/            principal, binding, token families
routing/             available-target snapshots and caches
providers/           built-in protocol adapters
stream/              normalized streaming state machine
managed/             host storage/control-plane ports
gateway/             HTTPS data/control adapters
sidecar/              protected framed IPC
cmd/llmkitd/          three-mode process
conformance/          reusable adapter contracts
specs/001-.../        approved product/runtime specification
```

**Structure Decision**: 保持单一 Go module；模式是 transport composition，不复制 Provider 实现。

