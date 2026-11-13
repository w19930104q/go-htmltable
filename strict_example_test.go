package htmltable_test

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/nfx/go-htmltable"
)

func ExampleNewStrictSliceFromString() {
	type Row struct {
		Name   string `header:"Name"`
		Active bool   `header:"Active"`
		Count  int    `header:"Count"`
	}
	// "yes" is not a legal strconv.ParseBool value and is reported instead
	// of being silently turned into false.
	html := `<table>
<tr><th>Name</th><th>Active</th><th>Count</th></tr>
<tr><td>alpha</td><td>true</td><td>10</td></tr>
<tr><td>beta</td><td>yes</td><td>20</td></tr>
<tr><td>gamma</td><td>false</td><td>many</td></tr>
</table>`

	out, err := htmltable.NewStrictSliceFromString[Row](html)
	var de *htmltable.DecodeError
	if errors.As(err, &de) {
		// no partial results are delivered
		fmt.Printf("out is nil: %v\n", out == nil)
		for _, ce := range de.Errors {
			fmt.Printf("%s (raw %q)\n", ce.Field, ce.Value)
		}
		// cell errors wrap the underlying conversion error
		fmt.Println(errors.Is(de.Errors[0], strconv.ErrSyntax))
	}
	// Output:
	// out is nil: true
	// Active (raw "yes")
	// Count (raw "many")
	// true
}

func ExampleNewStrictSliceFromString_nullablePointers() {
	type Row struct {
		Name  string  `header:"Name"`
		Note  *string `header:"Note"`
		Count *int    `header:"Count"`
	}
	html := `<table>
<tr><th>Name</th><th>Note</th><th>Count</th></tr>
<tr><td>alpha</td><td>first</td><td>10</td></tr>
<tr><td>beta</td><td></td><td></td></tr>
</table>`

	out, err := htmltable.NewStrictSliceFromString[Row](html)
	fmt.Println(err)
	fmt.Println(out[0].Name, *out[0].Note, *out[0].Count)
	fmt.Println(out[1].Name, out[1].Note == nil, out[1].Count == nil)
	// Output:
	// <nil>
	// alpha first 10
	// beta true true
}
