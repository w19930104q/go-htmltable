package htmltable

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// CellError describes a single failed cell conversion.
//
// Row is a zero-based data row index (the table header is not counted),
// Column is a zero-based column index within the table header. Value is
// the original, unmodified cell text.
type CellError struct {
	// Row is the zero-based index of the data row that failed.
	Row int
	// Column is the zero-based index of the column within the table header.
	Column int
	// Header is the table header name of the column.
	Header string
	// Field is the dotted path of the struct field, including
	// anonymous embedded structs (e.g. "Base.Name").
	Field string
	// Value is the original cell text that could not be converted.
	Value string
	// Err is the conversion error.
	Err error
}

func (e *CellError) Error() string {
	return fmt.Sprintf("row %d, column %d (%s -> %s): %q: %s",
		e.Row, e.Column, e.Header, e.Field, e.Value, e.Err.Error())
}

func (e *CellError) Unwrap() error {
	return e.Err
}

// DecodeError is returned by the strict constructors when at least one
// cell could not be converted. Errors are ordered by row, then by column
// position in the table header. No partially populated slice is returned
// together with this error.
type DecodeError struct {
	Errors []*CellError
}

func (e *DecodeError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d decode error(s):", len(e.Errors))
	for _, ce := range e.Errors {
		sb.WriteString("\n\t")
		sb.WriteString(ce.Error())
	}
	return sb.String()
}

func (e *DecodeError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, ce := range e.Errors {
		out[i] = ce
	}
	return out
}

// strictSlice is the explicitly opted-in strict decoding entry point.
// Unlike NewSlice, invalid values are never silently coerced: every
// conversion failure is collected and returned as a *DecodeError.
func strictSlice[T any](p *Page) ([]T, error) {
	var dummy T
	fields, err := planFields(reflect.TypeOf(dummy))
	if err != nil {
		return nil, err
	}
	table, err := findStrictTable(p.Tables, fields)
	if err != nil {
		return nil, err
	}
	columnOf := map[string]int{}
	for idx, header := range table.Header {
		columnOf[header] = idx
	}
	// deterministic cell ordering: by row first, then by column position
	sort.Slice(fields, func(i, j int) bool {
		return columnOf[fields[i].header] < columnOf[fields[j].header]
	})
	var cellErrors []*CellError
	sliceValue := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(dummy)),
		len(table.Rows), len(table.Rows))
	for rowIdx, row := range table.Rows {
		item := sliceValue.Index(rowIdx)
		for _, f := range fields {
			colIdx := columnOf[f.header]
			if colIdx >= len(row) {
				cellErrors = append(cellErrors, &CellError{
					Row: rowIdx, Column: colIdx, Header: f.header,
					Field: f.path, Value: "",
					Err: fmt.Errorf("short row: expected %d columns, got %d",
						colIdx+1, len(row)),
				})
				continue
			}
			raw := row[colIdx]
			target, err := allocField(item, f.index)
			if err != nil {
				cellErrors = append(cellErrors, &CellError{
					Row: rowIdx, Column: colIdx, Header: f.header,
					Field: f.path, Value: raw, Err: err,
				})
				continue
			}
			if err := decodeCell(target, raw); err != nil {
				cellErrors = append(cellErrors, &CellError{
					Row: rowIdx, Column: colIdx, Header: f.header,
					Field: f.path, Value: raw, Err: err,
				})
			}
		}
	}
	if len(cellErrors) > 0 {
		// never deliver partial results
		return nil, &DecodeError{Errors: cellErrors}
	}
	return sliceValue.Interface().([]T), nil
}

// findStrictTable selects the unique table whose header contains every
// mapped column exactly once. Unlike Page.FindWithColumns it performs no
// fuzzy counting, so missing columns, duplicate headers and ambiguous
// tables are reported precisely.
func findStrictTable(tables []*Table, fields []strictField) (*Table, error) {
	headers := make([]string, 0, len(fields))
	for _, f := range fields {
		headers = append(headers, f.header)
	}
	found := -1
	for idx, table := range tables {
		positions := map[string]int{}
		duplicate := map[string]struct{}{}
		for colIdx, header := range table.Header {
			if _, ok := positions[header]; ok {
				duplicate[header] = struct{}{}
			}
			positions[header] = colIdx
		}
		missing := false
		for _, header := range headers {
			if _, ok := positions[header]; !ok {
				missing = true
				break
			}
		}
		if missing {
			continue
		}
		if len(duplicate) > 0 {
			names := make([]string, 0, len(duplicate))
			for name := range duplicate {
				names = append(names, name)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("duplicate table header %q in table %d",
				strings.Join(names, ", "), idx)
		}
		if found >= 0 {
			return nil, fmt.Errorf("more than one table matches columns `%s`: "+
				"[%d] and [%d]", strings.Join(headers, ", "), found, idx)
		}
		found = idx
	}
	if found < 0 {
		return nil, fmt.Errorf("cannot find table with columns: %s",
			strings.Join(headers, ", "))
	}
	return tables[found], nil
}

// strictField is a resolved, writable destination for one table column.
type strictField struct {
	header string
	path   string
	index  []int
}

// planFields walks T (including anonymous embedded structs) and resolves
// every field carrying a `header` tag. It rejects everything that cannot
// be decoded safely up front, before any row is read.
func planFields(t reflect.Type) ([]strictField, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("strict decoding requires a struct type, got %s",
			t)
	}
	var fields []strictField
	headers := map[string]string{}
	var walk func(typ reflect.Type, prefix string, parent []int) error
	walk = func(typ reflect.Type, prefix string, parent []int) error {
		for i := 0; i < typ.NumField(); i++ {
			sf := typ.Field(i)
			path := sf.Name
			if prefix != "" {
				path = prefix + "." + sf.Name
			}
			index := append(append([]int(nil), parent...), i)
			if sf.Anonymous {
				embedded := sf.Type
				if embedded.Kind() == reflect.Ptr {
					if !sf.IsExported() {
						return fmt.Errorf("field %s cannot be set: unexported anonymous pointer",
							path)
					}
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					// an unexported anonymous struct embed still exposes
					// settable promoted leaf fields via index paths, so it
					// is safe to walk; allocField materializes nil pointer
					// embeds along the way
					if err := walk(embedded, path, index); err != nil {
						return err
					}
					continue
				}
			}
			header := sf.Tag.Get("header")
			if header == "" || header == "-" {
				continue
			}
			if !sf.IsExported() {
				return fmt.Errorf("field %s cannot be set: unexported field", path)
			}
			if err := isStrictTypeSupported(sf.Type, path); err != nil {
				return err
			}
			if other, ok := headers[header]; ok {
				return fmt.Errorf("duplicate header tag %q mapped to fields %s and %s",
					header, other, path)
			}
			headers[header] = path
			fields = append(fields, strictField{
				header: header,
				path:   path,
				index:  index,
			})
		}
		return nil
	}
	if err := walk(t, "", nil); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no fields with `header` tag found in %s", t)
	}
	return fields, nil
}

// isStrictTypeSupported validates the field type up front. Supported
// destinations are:
//   - pointer to any supported value type (nullable; an empty cell leaves nil),
//   - any type implementing encoding.TextUnmarshaler,
//   - string and all bool, signed/unsigned integer and float kinds.
func isStrictTypeSupported(t reflect.Type, path string) error {
	if t.Kind() == reflect.Ptr {
		if t.Elem().Kind() == reflect.Ptr {
			return fmt.Errorf("field %s has unsupported type %s: pointer-to-pointer",
				path, t)
		}
		if reflect.PointerTo(t.Elem()).Implements(textUnmarshalerType) {
			return nil
		}
		return isStrictTypeSupported(t.Elem(), path)
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return nil
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return nil
	}
	return fmt.Errorf("field %s has unsupported type %s", path, t)
}

var textUnmarshalerType = reflect.TypeOf((*interface {
	UnmarshalText([]byte) error
})(nil)).Elem()

// allocField walks the (possibly embedded, possibly pointer) field path
// and returns an addressable reflect.Value ready to be set. Every nil
// pointer encountered along the path is allocated.
func allocField(item reflect.Value, index []int) (v reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cannot reach field path %v: %v", index, r)
		}
	}()
	v = item
	for _, i := range index {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v, nil
}

// decodeCell converts raw cell text into target. A panic in any
// conversion (including a custom encoding.TextUnmarshaler) is recovered
// and reported as an ordinary error instead of crashing the process.
func decodeCell(target reflect.Value, raw string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic converting %q to %s: %v", raw, target.Type(), r)
		}
	}()
	typ := target.Type()
	// nullable pointer: empty cell stays nil, otherwise allocate and recurse
	if typ.Kind() == reflect.Ptr {
		if raw == "" {
			target.Set(reflect.Zero(typ))
			return nil
		}
		elem := reflect.New(typ.Elem()).Elem()
		if err := decodeCell(elem, raw); err != nil {
			return err
		}
		target.Set(elem.Addr())
		return nil
	}
	// encoding.TextUnmarshaler (pointer receiver is available because
	// every destination is addressable)
	if target.Addr().Type().Implements(textUnmarshalerType) {
		return target.Addr().Interface().(interface {
			UnmarshalText([]byte) error
		}).UnmarshalText([]byte(raw))
	}
	switch typ.Kind() {
	case reflect.String:
		target.SetString(raw)
		return nil
	case reflect.Bool:
		// strict bool: exactly the strconv.ParseBool accepted values,
		// no silent coercion of unknown text to false
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		target.SetBool(v)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, bitSize(typ))
		if err != nil {
			return err
		}
		target.SetInt(v)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v, err := strconv.ParseUint(raw, 10, bitSize(typ))
		if err != nil {
			return err
		}
		target.SetUint(v)
		return nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, bitSize(typ))
		if err != nil {
			return err
		}
		target.SetFloat(v)
		return nil
	default:
		return fmt.Errorf("unsupported type %s", typ)
	}
}

func bitSize(t reflect.Type) int {
	switch t.Kind() {
	case reflect.Int8, reflect.Uint8:
		return 8
	case reflect.Int16, reflect.Uint16:
		return 16
	case reflect.Int32, reflect.Uint32, reflect.Float32:
		return 32
	case reflect.Int64, reflect.Uint64, reflect.Float64:
		return 64
	case reflect.Int, reflect.Uint, reflect.Uintptr:
		return strconv.IntSize
	}
	return 0
}

// NewStrictSlice returns a slice of annotated structs decoded from r under
// strict rules: invalid booleans, numbers, overflows, short rows and
// failing custom conversions are collected into a *DecodeError instead of
// being silently coerced or skipped. On any error the returned slice is nil.
//
// Struct fields may be anonymous embedded structs (values or pointers),
// nullable pointers and types implementing encoding.TextUnmarshaler.
// An empty cell leaves a pointer field nil and sets a string field to "".
// Missing columns, duplicate table headers and conflicting field mappings
// are rejected before any row is processed.
func NewStrictSlice[T any](ctx context.Context, r io.Reader) ([]T, error) {
	p, err := New(ctx, r)
	if err != nil {
		return nil, err
	}
	return NewStrictSliceFromPage[T](p)
}

// NewStrictSliceFromPage is NewStrictSlice against an already parsed Page.
func NewStrictSliceFromPage[T any](p *Page) ([]T, error) {
	return strictSlice[T](p)
}

// NewStrictSliceFromString is NewStrictSlice from a string.
func NewStrictSliceFromString[T any](in string) ([]T, error) {
	return NewStrictSlice[T](context.Background(), strings.NewReader(in))
}

// NewStrictSliceFromResponse is NewStrictSlice from an http.Response.
// Underlying transport and body read errors are returned unchanged.
func NewStrictSliceFromResponse[T any](resp *http.Response) ([]T, error) {
	p, err := NewFromResponse(resp)
	if err != nil {
		return nil, err
	}
	return NewStrictSliceFromPage[T](p)
}

// NewStrictSliceFromURL is NewStrictSlice from an URL.
// Underlying transport errors are returned unchanged.
func NewStrictSliceFromURL[T any](url string) ([]T, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	return NewStrictSliceFromResponse[T](resp)
}
