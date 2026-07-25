# Contributing

1. Discuss substantial format or compatibility changes in an issue first.
2. Keep identifiers, filenames, directories, comments, documentation, commit
   messages, and user-facing strings in English.
3. Run `go fmt`, `go vet`, and the complete test suite before opening a pull
   request.
4. Add tests for every behavior change and preserve backward compatibility for
   published `.vipr` format versions.
5. Keep cgo code small, defensive, and isolated under `internal/zstd`.

## Local checks

```sh
./scripts/fetch-zstd.sh
./scripts/build-zstd.sh
go test -tags vipr_static_zstd ./...
go vet -tags vipr_static_zstd ./...
```

Contributions are accepted under the repository's MIT license.
