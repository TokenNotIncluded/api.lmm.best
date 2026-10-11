# Frontend compatibility fixtures

The feature-local JSON fixtures retain the previous frontend contract coverage
without restoring removed monolith packages:

- `src/features/system-settings/content/testdata/assistant-tool-catalog.json`
  contains the 17 groups and all 72 tool definitions from
  `apps/api-go/setting/assistant_tool_policy.go` at
  `5117b2a2272e44f51b368ac6c68877def799f256`. The editor tests validate the
  catalog schema and exercise every tool with and without advanced rules.
- `src/features/usage-logs/lib/__tests__/testdata/response-model-compatibility.json`
  preserves all 24 cases from
  `apps/api-go/relay/common/testdata/response_model_compatibility.json` at that
  same revision, including supported aliases and incompatible model variants.

These are historical client compatibility examples, not an authoritative WIP
extension catalog or proof of live Go/Rust contract parity. When a new service
implements these APIs, add producer-side contract verification against its actual
implementation rather than reading or restoring a removed backend source path.

The fixtures remain under the repository's AGPL-3.0-or-later license and existing
source attribution. They contain no credentials or production records.
