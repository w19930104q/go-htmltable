package htmltable

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type embeddedBase struct {
	Name  string     `header:"Name"`
	Since *time.Time `header:"Since"`
}

type strictRow struct {
	embeddedBase
	Active bool    `header:"Active"`
	Count  int     `header:"Count"`
	Price  float64 `header:"Price"`
	Note   *string `header:"Note"`
}

func ptr[T any](v T) *T { return &v }

func loadStrictFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/strict.html")
	assertNoError(t, err)
	return string(b)
}

func TestNewStrictSlice_HappyPath(t *testing.T) {
	fixture := loadStrictFixture(t)
	out, err := NewStrictSliceFromString[strictRow](fixture)
	assertNoError(t, err)
	t1 := time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC)
	t2 := time.Date(2024, 2, 3, 15, 4, 5, 0, time.UTC)
	t3 := time.Date(2024, 3, 4, 15, 4, 5, 0, time.UTC)
	assertEqual(t, []strictRow{
		{
			embeddedBase: embeddedBase{Name: "alpha", Since: &t1},
			Active:       true, Count: 10, Price: 1.5, Note: ptr("first"),
		},
		{
			embeddedBase: embeddedBase{Name: "beta", Since: &t2},
			Active:       false, Count: 20, Price: 2.5, Note: nil, // empty cell -> nil pointer
		},
		{
			embeddedBase: embeddedBase{Name: "gamma", Since: &t3},
			Active:       true, Count: -30, Price: -3.25, Note: ptr("third"),
		},
	}, out)
}

func TestNewStrictSlice_BoolParseBoolSet(t *testing.T) {
	html := `<table><tr><th>v</th></tr>
<tr><td>1</td></tr><tr><td>T</td></tr><tr><td>TRUE</td></tr>
<tr><td>True</td></tr><tr><td>t</td></tr>
<tr><td>0</td></tr><tr><td>F</td></tr><tr><td>FALSE</td></tr>
<tr><td>False</td></tr><tr><td>f</td></tr></table>`
	type b struct {
		V bool `header:"v"`
	}
	out, err := NewStrictSliceFromString[b](html)
	assertNoError(t, err)
	assertEqual(t, []bool{true, true, true, true, true,
		false, false, false, false, false},
		[]bool{out[0].V, out[1].V, out[2].V, out[3].V, out[4].V,
			out[5].V, out[6].V, out[7].V, out[8].V, out[9].V})
}

func TestNewStrictSlice_InvalidBoolIsErrorNotFalse(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Active</th></tr>
<tr><td>alpha</td><td>yes</td></tr></table>`
	type row struct {
		Name   string `header:"Name"`
		Active bool   `header:"Active"`
	}
	out, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DecodeError, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil slice on error, got %v", out)
	}
	if len(de.Errors) != 1 {
		t.Fatalf("expected 1 cell error, got %d: %v", len(de.Errors), de.Errors)
	}
	ce := de.Errors[0]
	assertEqual(t, 0, ce.Row)
	assertEqual(t, 1, ce.Column)
	assertEqual(t, "Active", ce.Header)
	assertEqual(t, "Active", ce.Field)
	assertEqual(t, "yes", ce.Value)
	if !strings.Contains(ce.Err.Error(), "invalid syntax") {
		t.Errorf("expected parse error, got %v", ce.Err)
	}
	// errors must be unwrappable for errors.As/Is chains
	var numErr *strconv.NumError
	if !errors.As(ce, &numErr) {
		t.Errorf("expected unwrap to *strconv.NumError, got %v", ce.Err)
	}
}

func TestNewStrictSlice_ErrorsOrderedByRowAndColumn(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Active</th><th>Count</th></tr>
<tr><td>a</td><td>yes</td><td>abc</td></tr>
<tr><td>b</td><td>maybe</td><td>12</td></tr></table>`
	type row struct {
		Name   string `header:"Name"`
		Active bool   `header:"Active"`
		Count  int    `header:"Count"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DecodeError, got %v", err)
	}
	got := [][2]int{}
	for _, ce := range de.Errors {
		got = append(got, [2]int{ce.Row, ce.Column})
	}
	assertEqual(t, [][2]int{{0, 1}, {0, 2}, {1, 1}}, got)
}

func TestNewStrictSlice_DiagnosticsStableAcrossRuns(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Active</th><th>Count</th></tr>
<tr><td>a</td><td>yes</td><td>abc</td></tr>
<tr><td>b</td><td>maybe</td><td>12</td></tr></table>`
	type row struct {
		Name   string `header:"Name"`
		Active bool   `header:"Active"`
		Count  int    `header:"Count"`
	}
	var first *DecodeError
	for i := 0; i < 3; i++ {
		_, err := NewStrictSliceFromString[row](html)
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Fatalf("run %d: expected *DecodeError, got %v", i, err)
		}
		if first == nil {
			first = de
			continue
		}
		assertEqual(t, first.Error(), de.Error())
	}
}

func TestNewStrictSlice_ShortRow(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Active</th><th>Count</th></tr>
<tr><td>a</td><td>true</td><td>1</td></tr>
<tr><td>b</td><td>false</td></tr></table>`
	type row struct {
		Name   string `header:"Name"`
		Active bool   `header:"Active"`
		Count  int    `header:"Count"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	ce := de.Errors[0]
	assertEqual(t, 1, ce.Row)
	assertEqual(t, 2, ce.Column)
	assertEqual(t, "Count", ce.Header)
	assertEqual(t, "Count", ce.Field)
	assertEqual(t, "", ce.Value)
	if !strings.Contains(ce.Err.Error(), "short row") {
		t.Errorf("expected short row error, got %v", ce.Err)
	}
}

func TestNewStrictSlice_EmbeddedFieldPath(t *testing.T) {
	html := `<table><tr><th>Since</th></tr><tr><td>not-a-timestamp</td></tr></table>`
	type localBase struct {
		Since *time.Time `header:"Since"`
	}
	type localRow struct {
		localBase
	}
	_, err := NewStrictSliceFromString[localRow](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	assertEqual(t, "localBase.Since", de.Errors[0].Field)
	assertEqual(t, "Since", de.Errors[0].Header)
}

func TestNewStrictSlice_DuplicateTableHeaderRejected(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Name</th></tr>
<tr><td>a</td><td>b</td></tr></table>`
	type row struct {
		Name string `header:"Name"`
	}
	_, err := NewStrictSliceFromString[row](html)
	assertError(t, err)
	if !strings.Contains(err.Error(), "duplicate table header") {
		t.Fatalf("expected duplicate table header error, got %v", err)
	}
}

func TestNewStrictSlice_MissingColumnRejected(t *testing.T) {
	type row struct {
		Name  string `header:"Name"`
		Ghost string `header:"Ghost"`
	}
	_, err := NewStrictSliceFromString[row](loadStrictFixture(t))
	assertEqualError(t, err, "cannot find table with columns: Name, Ghost")
}

func TestNewStrictSlice_MissingColumnWithPresentTable(t *testing.T) {
	html := `<table><tr><th>Present</th></tr><tr><td>x</td></tr></table>
<table><tr><th>Name</th></tr><tr><td>a</td></tr></table>`
	type row struct {
		Name  string `header:"Name"`
		Ghost string `header:"Ghost"`
	}
	_, err := NewStrictSliceFromString[row](html)
	assertEqualError(t, err, "cannot find table with columns: Name, Ghost")
}

func TestNewStrictSlice_DuplicateHeaderTagRejected(t *testing.T) {
	type row struct {
		A string `header:"Name"`
		B string `header:"Name"`
	}
	_, err := NewStrictSliceFromString[row](loadStrictFixture(t))
	assertError(t, err)
	if !strings.Contains(err.Error(), "duplicate header tag") {
		t.Fatalf("expected duplicate header tag error, got %v", err)
	}
}

func TestNewStrictSlice_NonStructRejected(t *testing.T) {
	_, err := NewStrictSliceFromString[int](loadStrictFixture(t))
	assertEqualError(t, err, "strict decoding requires a struct type, got int")
}

func TestNewStrictSlice_NoHeaderTagsRejected(t *testing.T) {
	type row struct {
		Name string
	}
	_, err := NewStrictSliceFromString[row](loadStrictFixture(t))
	assertEqualError(t, err, "no fields with `header` tag found in htmltable.row")
}

func TestNewStrictSlice_UnwritableFieldRejected(t *testing.T) {
	type row struct {
		name string `header:"Name"` //nolint:unused
	}
	_, err := NewStrictSliceFromString[row](loadStrictFixture(t))
	assertEqualError(t, err, "field name cannot be set: unexported field")
}

func TestNewStrictSlice_UnsupportedTypeRejected(t *testing.T) {
	type row struct {
		Mapping map[string]string `header:"Name"`
	}
	_, err := NewStrictSliceFromString[row](loadStrictFixture(t))
	assertEqualError(t, err, "field Mapping has unsupported type map[string]string")
}

func TestNewStrictSlice_NumericOverflowReported(t *testing.T) {
	html := `<table><tr><th>Count</th></tr><tr><td>128</td></tr></table>`
	type row struct {
		Count int8 `header:"Count"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	if !errors.Is(de.Errors[0].Err, strconv.ErrRange) {
		t.Errorf("expected strconv.ErrRange, got %v", de.Errors[0].Err)
	}
}

func TestNewStrictSlice_EmptyCellOnScalarIsError(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Count</th></tr>
<tr><td>a</td><td></td></tr></table>`
	type row struct {
		Name  string `header:"Name"`
		Count int    `header:"Count"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	ce := de.Errors[0]
	assertEqual(t, 1, ce.Column)
	assertEqual(t, "", ce.Value)
}

func TestNewStrictSlice_EmptyStringCellStaysEmpty(t *testing.T) {
	// whitespace-only cells trim to ""; a row of truly empty cells is
	// treated as a table divider by the parser (pre-existing behavior)
	html := `<table><tr><th>Name</th><th>Other</th></tr>
<tr><td>&nbsp;</td><td>keep</td></tr></table>`
	type row struct {
		Name  string `header:"Name"`
		Other string `header:"Other"`
	}
	out, err := NewStrictSliceFromString[row](html)
	assertNoError(t, err)
	if len(out) != 1 {
		t.Fatalf("expected one row, got %d", len(out))
	}
	assertEqual(t, "", out[0].Name)
	assertEqual(t, "keep", out[0].Other)
}

type failingUnmarshaler struct{}

func (*failingUnmarshaler) UnmarshalText([]byte) error {
	return fmt.Errorf("custom conversion rejected")
}

func TestNewStrictSlice_CustomTextUnmarshalerFailure(t *testing.T) {
	html := `<table><tr><th>v</th></tr><tr><td>xyz</td></tr></table>`
	type row struct {
		V failingUnmarshaler `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	assertEqual(t, "custom conversion rejected", de.Errors[0].Err.Error())
	assertEqual(t, "xyz", de.Errors[0].Value)
}

type panickingUnmarshaler struct{}

func (*panickingUnmarshaler) UnmarshalText([]byte) error {
	panic("boom in custom unmarshaler")
}

func TestNewStrictSlice_CustomUnmarshalerPanicDoesNotPanic(t *testing.T) {
	html := `<table><tr><th>v</th></tr><tr><td>xyz</td></tr></table>`
	type row struct {
		V panickingUnmarshaler `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) || len(de.Errors) != 1 {
		t.Fatalf("expected one *DecodeError, got %v", err)
	}
	if !strings.Contains(de.Errors[0].Err.Error(), "panic") {
		t.Errorf("expected panic to be recovered as error, got %v", de.Errors[0].Err)
	}
}

type InnerEmbed struct {
	Score *int `header:"Score"`
}

type outerEmbed struct {
	*InnerEmbed
	Name string `header:"Name"`
}

func TestNewStrictSlice_PointerEmbeddedStructAndNullableInt(t *testing.T) {
	html := `<table><tr><th>Name</th><th>Score</th></tr>
<tr><td>a</td><td>42</td></tr>
<tr><td>b</td><td></td></tr></table>`
	out, err := NewStrictSliceFromString[outerEmbed](html)
	assertNoError(t, err)
	if out[0].InnerEmbed == nil || out[0].Score == nil || *out[0].Score != 42 {
		t.Fatalf("expected allocated embedded pointer with score 42, got %+v", out[0])
	}
	if out[1].InnerEmbed != nil && out[1].Score != nil {
		t.Fatalf("expected nil score for empty cell, got %+v", out[1])
	}
	assertEqual(t, "b", out[1].Name)
}

func TestNewStrictSlice_UnexportedPointerEmbedRejected(t *testing.T) {
	type hidden struct {
		V string `header:"v"`
	}
	type row struct {
		*hidden
	}
	_, err := NewStrictSliceFromString[row](`<table><tr><th>v</th></tr><tr><td>x</td></tr></table>`)
	assertEqualError(t, err, "field hidden cannot be set: unexported anonymous pointer")
}

func TestNewStrictSlice_PointerToPointerRejected(t *testing.T) {
	type row struct {
		V **string `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](`<table><tr><th>v</th></tr><tr><td>x</td></tr></table>`)
	assertEqualError(t, err, "field V has unsupported type **string: pointer-to-pointer")
}

func TestNewStrictSlice_DuplicateHeaderInMatchingTableRejected(t *testing.T) {
	html := `<table><tr><th>v</th><th>v</th></tr><tr><td>a</td><td>b</td></tr></table>`
	type row struct {
		V string `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](html)
	assertEqualError(t, err, "duplicate table header \"v\" in table 0")
}

func TestNewStrictSlice_AmbiguousTablesRejected(t *testing.T) {
	html := `<table><tr><th>v</th></tr><tr><td>a</td></tr></table>
<table><tr><th>v</th></tr><tr><td>b</td></tr></table>`
	type row struct {
		V string `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](html)
	assertEqualError(t, err,
		"more than one table matches columns `v`: [0] and [1]")
}

func TestNewStrictSlice_UintAndFloatKinds(t *testing.T) {
	html := `<table><tr><th>u</th><th>f32</th><th>f64</th></tr>
<tr><td>255</td><td>1.25</td><td>3.14159</td></tr></table>`
	type row struct {
		U   uint8   `header:"u"`
		F32 float32 `header:"f32"`
		F64 float64 `header:"f64"`
	}
	out, err := NewStrictSliceFromString[row](html)
	assertNoError(t, err)
	assertEqual(t, uint8(255), out[0].U)
	assertEqual(t, float32(1.25), out[0].F32)
	assertEqual(t, 3.14159, out[0].F64)
}

func TestNewStrictSlice_DecodeErrorUnwrap(t *testing.T) {
	html := `<table><tr><th>v</th></tr><tr><td>maybe</td></tr></table>`
	type row struct {
		V bool `header:"v"`
	}
	_, err := NewStrictSliceFromString[row](html)
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DecodeError, got %v", err)
	}
	unwrapped := de.Unwrap()
	if len(unwrapped) != 1 {
		t.Fatalf("expected one unwrapped error, got %d", len(unwrapped))
	}
	if !errors.Is(err, strconv.ErrSyntax) {
		t.Errorf("expected errors.Is to reach strconv.ErrSyntax, got %v", err)
	}
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestNewStrictSlice_UnderlyingReadErrorPreserved(t *testing.T) {
	want := errors.New("synthetic read failure")
	_, err := NewStrictSlice[strictRow](context.Background(), errReader{err: want})
	if err == nil || !strings.Contains(err.Error(), "synthetic read failure") {
		t.Fatalf("expected read error to be preserved, got %v", err)
	}
}

func TestNewStrictSlice_AllEntriesReturnIdenticalResults(t *testing.T) {
	fixture := loadStrictFixture(t)
	want, err := NewStrictSliceFromString[strictRow](fixture)
	assertNoError(t, err)

	fromReader, err := NewStrictSlice[strictRow](context.Background(), strings.NewReader(fixture))
	assertNoError(t, err)
	assertEqual(t, want, fromReader)

	page, err := NewFromString(fixture)
	assertNoError(t, err)
	fromPage, err := NewStrictSliceFromPage[strictRow](page)
	assertNoError(t, err)
	assertEqual(t, want, fromPage)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, fixture)
	}))
	defer server.Close()

	fromURL, err := NewStrictSliceFromURL[strictRow](server.URL)
	assertNoError(t, err)
	assertEqual(t, want, fromURL)

	resp, err := http.Get(server.URL)
	assertNoError(t, err)
	fromResponse, err := NewStrictSliceFromResponse[strictRow](resp)
	_ = resp.Body.Close()
	assertNoError(t, err)
	assertEqual(t, want, fromResponse)
}

func TestNewSlice_LegacyLenientBehaviorUnchanged(t *testing.T) {
	// old entry point still coerces unknown bool text silently to false
	html := `<table><tr><th>a</th></tr>
<tr><td>yes</td></tr><tr><td>maybe</td></tr></table>`
	type row struct {
		A bool `header:"a"`
	}
	out, err := NewSliceFromString[row](html)
	assertNoError(t, err)
	assertEqual(t, true, out[0].A)
	assertEqual(t, false, out[1].A)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (failingBody) Close() error             { return nil }

func TestNewStrictSliceFromResponse_BodyReadErrorPreserved(t *testing.T) {
	resp := &http.Response{
		Request: &http.Request{},
		Body:    failingBody{},
	}
	_, err := NewStrictSliceFromResponse[strictRow](resp)
	if err == nil || !strings.Contains(err.Error(), "body read failed") {
		t.Fatalf("expected body read error, got %v", err)
	}
}

func TestNewStrictSliceFromURL_NoMatchingTable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<table><tr><th>unrelated</th></tr></table>`)
	}))
	defer server.Close()
	_, err := NewStrictSliceFromURL[strictRow](server.URL)
	assertEqualError(t, err,
		"cannot find table with columns: Name, Since, Active, Count, Price, Note")
}

func TestNewStrictSlice_FixedHTMLFixtureRegression(t *testing.T) {
	// the checked-in fixture must keep decoding identically
	fixture := loadStrictFixture(t)
	first, err := NewStrictSliceFromString[strictRow](fixture)
	assertNoError(t, err)
	second, err := NewStrictSlice[strictRow](context.Background(), strings.NewReader(fixture))
	assertNoError(t, err)
	assertEqual(t, first, second)
	if len(first) != 3 {
		t.Fatalf("expected 3 fixture rows, got %d", len(first))
	}
}

// keep io referenced even if helper signatures evolve
var _ io.Reader = errReader{}
