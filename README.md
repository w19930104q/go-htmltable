# HTML table data extractor for Go

[![GoDoc](https://img.shields.io/badge/go-documentation-blue.svg)](https://pkg.go.dev/mod/github.com/nfx/go-htmltable)
[![MIT license](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/nfx/go-htmltable/blob/main/LICENSE)
[![codecov](https://codecov.io/gh/nfx/go-htmltable/branch/main/graph/badge.svg)](https://codecov.io/gh/nfx/go-htmltable)
[![build](https://github.com/nfx/go-htmltable/workflows/build/badge.svg?branch=main)](https://github.com/nfx/go-htmltable/actions?query=workflow%3Abuild+branch%3Amain)


`htmltable` enables structured data extraction from HTML tables and URLs and requires almost no external dependencies.

## Installation

```bash
go get github.com/nfx/go-htmltable
```

## Usage

You can retrieve a slice of `header`-annotated types using the `NewSlice*` contructors:

```go
type Ticker struct {
    Symbol   string `header:"Symbol"`
    Security string `header:"Security"`
    CIK      string `header:"CIK"`
}

url := "https://en.wikipedia.org/wiki/List_of_S%26P_500_companies"
out, _ := htmltable.NewSliceFromURL[Ticker](url)
fmt.Println(out[0].Symbol)
fmt.Println(out[0].Security)

// Output: 
// MMM
// 3M
```

An error would be thrown if there's no matching page with the specified columns:

```go
page, _ := htmltable.NewFromURL("https://en.wikipedia.org/wiki/List_of_S%26P_500_companies")
_, err := page.FindWithColumns("invalid", "column", "names")
fmt.Println(err)

// Output: 
// cannot find table with columns: invalid, column, names
```

And you can use more low-level API to work with extracted data:

```go
page, _ := htmltable.NewFromString(`<body>
    <h1>foo</h2>
    <table>
        <tr><td>a</td><td>b</td></tr>
        <tr><td> 1 </td><td>2</td></tr>
        <tr><td>3  </td><td>4   </td></tr>
    </table>
    <h1>bar</h2>
    <table>
        <tr><th>b</th><th>c</th><th>d</th></tr>
        <tr><td>1</td><td>2</td><td>5</td></tr>
        <tr><td>3</td><td>4</td><td>6</td></tr>
    </table>
</body>`)

fmt.Printf("found %d tables\n", page.Len())
_ = page.Each2("c", "d", func(c, d string) error {
    fmt.Printf("c:%s d:%s\n", c, d)
    return nil
})

// Output: 
// found 2 tables
// c:2 d:5
// c:4 d:6
```

You can also range over rows with `Iter`, which yields values in the order of the requested columns:

```go
for row, err := range page.Iter("c", "d") {
    if err != nil {
        panic(err)
    }
    fmt.Printf("c:%s d:%s\n", row[0], row[1])
}

// Output: 
// c:2 d:5
// c:4 d:6
```

Complex [tables with row and col spans](https://en.wikipedia.org/wiki/List_of_AMD_chipsets#AM4_chipsets) are natively supported as well. You can annotate `string`, `int`, and `bool` fields. Any `bool` field value is `true` if it is equal in lowercase to one of `yes`, `y`, `true`, `t`.

![Wikipedia, AMD AM4 chipsets](doc/colspans-rowspans.png)

```go
type AM4 struct {
    Model             string `header:"Model"`
    ReleaseDate       string `header:"Release date"`
    PCIeSupport       string `header:"PCIesupport[a]"`
    MultiGpuCrossFire bool   `header:"Multi-GPU CrossFire"`
    MultiGpuSLI       bool   `header:"Multi-GPU SLI"`
    USBSupport        string `header:"USBsupport[b]"`
    SATAPorts         int    `header:"Storage features SATAports"`
    RAID              string `header:"Storage features RAID"`
    AMDStoreMI        bool   `header:"Storage features AMD StoreMI"`
    Overclocking      string `header:"Processoroverclocking"`
    TDP               string `header:"TDP"`
    SupportExcavator  string `header:"CPU support[14] Excavator"`
    SupportZen        string `header:"CPU support[14] Zen"`
    SupportZenPlus    string `header:"CPU support[14] Zen+"`
    SupportZen2       string `header:"CPU support[14] Zen 2"`
    SupportZen3       string `header:"CPU support[14] Zen 3"`
    Architecture      string `header:"Architecture"`
}
am4Chipsets, _ := htmltable.NewSliceFromURL[AM4]("https://en.wikipedia.org/wiki/List_of_AMD_chipsets")
fmt.Println(am4Chipsets[2].Model)
fmt.Println(am4Chipsets[2].SupportZen2)

// Output: 
// X370
// Varies[c]
```

### Strict decoding

The `NewSlice*` constructors stay lenient for backwards compatibility: unknown
boolean text becomes `false` and short rows are skipped. When a table feeds a
typed data pipeline where silent coercion is unacceptable, opt in to the
strict constructors instead: `NewStrictSlice`, `NewStrictSliceFromString`,
`NewStrictSliceFromResponse`, `NewStrictSliceFromURL` and
`NewStrictSliceFromPage`.

Strict decoding adds:

- **anonymous embedded structs**, both value and pointer embeds (nil pointer
  embeds are allocated on demand), with dotted field paths such as `Base.Name`
  in diagnostics;
- **nullable pointer fields** — an empty cell leaves the pointer as `nil`,
  otherwise the pointed-to value is parsed;
- types implementing `encoding.TextUnmarshaler` (e.g. `time.Time`), including
  pointer fields;
- all integer kinds (signed and unsigned, every width), floats and strings,
  with numeric overflow reported via `strconv.ErrRange`;
- **strict booleans**: only the values accepted by `strconv.ParseBool`
  (`1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False`) are valid;
- structural rejection, before any row is processed, of non-struct target
  types, unwritable fields, unsupported field types, duplicate `header` tags,
  duplicate table headers, missing columns and ambiguous matching tables;
- a guarantee that no failure path panics — panics inside a custom
  `UnmarshalText` are recovered and reported as cell errors.

Every conversion failure across all rows is collected into a
`*htmltable.DecodeError` whose `Errors` are ordered by row, then by column.
Each `*htmltable.CellError` carries the zero-based row and column, the table
header, the struct field path, the original cell text and the wrapped
conversion error. On any error the returned slice is `nil` — partial results
are never delivered. Underlying I/O and HTML parse errors are returned
unchanged.

```go
type Row struct {
    Name   string `header:"Name"`
    Active bool   `header:"Active"`
    Count  int    `header:"Count"`
}

out, err := htmltable.NewStrictSliceFromString[Row](`
<table>
  <tr><th>Name</th><th>Active</th><th>Count</th></tr>
  <tr><td>alpha</td><td>true</td><td>10</td></tr>
  <tr><td>beta</td><td>yes</td><td>20</td></tr>
  <tr><td>gamma</td><td>false</td><td>many</td></tr>
</table>`)

var de *htmltable.DecodeError
if errors.As(err, &de) {
    fmt.Println(out == nil) // true: no partial results
    for _, ce := range de.Errors {
        fmt.Printf("row %d col %d %s: %q failed: %v\n",
            ce.Row, ce.Column, ce.Field, ce.Value, ce.Err)
    }
}

// Output:
// true
// row 1 col 1 Active: "yes" failed: strconv.ParseBool: parsing "yes": invalid syntax
// row 2 col 2 Count: "many" failed: strconv.ParseInt: parsing "many": invalid syntax
```

And the last note: you're encouraged to plug your own structured logger:

```go
htmltable.Logger = func(_ context.Context, msg string, fields ...any) {
    fmt.Printf("[INFO] %s %v\n", msg, fields)
}
htmltable.NewFromURL("https://en.wikipedia.org/wiki/List_of_S%26P_500_companies")

// Output:
// [INFO] found table [columns [Symbol Security SEC filings GICSSector GICS Sub-Industry Headquarters Location Date first added CIK Founded] count 504]
// [INFO] found table [columns [Date Added Ticker Added Security Removed Ticker Removed Security Reason] count 308]
```

## Inspiration

This library aims to be something like [pandas.read_html](https://pandas.pydata.org/docs/reference/api/pandas.read_html.html) or [table_extract](https://docs.rs/table-extract/latest/table_extract/) Rust crate, but more idiomatic for Go.