# External Consumer Example

This example is a standalone Go module that imports:

- `github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel`

without any local `replace` directives.

## Verify

From this directory:

```bash
go test ./...
go run .
```
