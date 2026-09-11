# Local agentcore mirror

This directory mirrors `github.com/voocel/agentcore` v1.8.1.

Local patch:

- Preserve complete tool execution errors in `ProgressToolError` for host diagnostics, with an explicit 64 KiB safety cap instead of the upstream silent 200-byte display truncation.

Keep the contract tests in `internal/agents/agentcore_contract_test.go` green when updating this mirror.
