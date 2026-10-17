# CSV Reader

Package `reader` reads raw CSV records from an `io.ReaderAt` and parses an individual record into fields. It is part of the `github.com/codeindex2937/csv` module.

## Reading records

`NewReader` accepts an `io.ReaderAt`, such as a `*bytes.Reader` or `*os.File`. `ReadRecord` returns an `iter.Seq[string]`; each yielded string contains one raw record without its terminating line ending. Newlines inside quoted fields are retained.

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/codeindex2937/csv/reader"
)

func main() {
	r := reader.NewReader(bytes.NewReader([]byte(
		"name,description\n" +
			"Example,\"A value with a comma, and\nan embedded newline\"\n",
	)))

	for record := range r.ReadRecord() {
		fields, err := reader.ParseFields(record)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%q\n", fields)
	}
}
```

## Parsing a record

`ParseFields` parses one raw record and returns its fields in order. It supports comma-separated fields, quoted fields, doubled quotes (`""`) as an escaped quote, and newlines inside quoted fields.

```go
fields, err := reader.ParseFields(`id,"a, quoted value","say ""hello"""`)
if err != nil {
	panic(err)
}
fmt.Println(fields) // [id a, quoted value say "hello"]
```

An unterminated quoted field, a quote inside an unquoted field, or characters between a closing quote and the next comma produce an error. `ParseFields` parses one record; use `ReadRecord` to split a multi-record input while respecting quoted newlines.

## Mapping rows to a struct

`NewStructReader[T]` reads the first record as a header, builds a Go field-to-header mapping from each exported field's `json` tag (or its Go field name when no tag is set), then resolves those headers to column indexes. `Read` returns an `iter.Seq[T]` over the decoded data rows:

```go
type Quote struct {
	Symbol string  `json:"symbol"`
	Price  float64 `json:"price"`
	Volume int64   `json:"volume"`
}

r, err := reader.NewStructReader[Quote](reader.NewReader(bytes.NewReader([]byte(
	"volume,symbol,price\n1000,ACME,12.5\n",
))))
if err != nil {
	panic(err)
}

for quote := range r.Read() {
	fmt.Printf("%+v\n", quote)
}
```

The column order can differ from the struct field order. Unmatched CSV columns are ignored; struct fields with no matching header remain zero-valued. A `json:"-"` field is never populated, and a field without an explicit JSON name uses its Go field name. Strings, booleans, numeric values, pointers to supported values, and fields implementing `encoding.TextUnmarshaler` are supported. This includes `time.Time`, which accepts RFC3339 timestamps (for example, `2026-10-10T16:48:27+08:00`). An empty value sets a pointer field to `nil`; empty values for non-string scalar fields return a conversion error. Duplicate headers are rejected when constructing the reader. A row with the wrong number of columns or an unsupported field type stops iteration.

Iteration stops if a record cannot be parsed or a field cannot be converted. The current `StructReader` API does not expose its stored iteration error, so callers cannot distinguish a clean end of input from an iteration stopped by an error.

### Mapping an existing row iterator by index

`CollectByIndex[T]` accepts a map of Go struct field names or JSON tags to indexes in each row. It returns an `iter.Seq2` yielding the decoded struct and original row:

```go
rows := iter.Seq[[]string](func(yield func([]string) bool) {
	for _, row := range data {
		if !yield(row) {
			return
		}
	}
})

for quote, row := range reader.CollectByIndex[Quote](
	map[string]int{"Volume": 0, "Symbol": 1, "Price": 2},
	rows,
) {
	fmt.Printf("%+v from %v\n", quote, row)
}
```

Unknown field names are ignored. Invalid column indexes or field conversion failures cause `CollectByIndex` to panic during iteration.

## Errors and limitations

`ReadRecord` stops when it reaches EOF or encounters a read/format error. The current API does not expose its stored error outside the package, so callers cannot distinguish clean EOF from an iteration that stopped on a read/format error. `ParseFields` returns errors for malformed records it receives.

The reader uses a comma delimiter and CSV-style double-quote escaping. It does not provide the configurable options or complete compatibility guarantees of Go's `encoding/csv` package.

## Tests

From the module root (`deps/csv`), run:

```powershell
go test ./reader
```
