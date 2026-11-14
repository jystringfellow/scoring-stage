# Build and format with Go 1.27

Go releases can change gofmt's alignment: Go 1.27 formats the gear tests differently from Go 1.26.
The module now requires Go 1.27, matching the example Go book. CI and release builds read the version from
go.mod, and CI already checks gofmt before vet and tests. Evidence: Go 1.27.3's `gofmt -l .` is empty.
