#####
* Implement Tool interface
```go
type Tool interface {
    Name() string
    Description() string
    Execute(ctx context.Context ,args json.RawMessage) error
}
```
* So we need to convert our existing av fetcher and indiasm fetcher to tools. @fetcher/av @fetcher/indiasm
* DB storage and Archivus client storage should be called within the fetcher tool
* Also convert metrics calculation to tool @internal/metrics

