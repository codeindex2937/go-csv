package reader

import (
	"bytes"
	"errors"
	"io"
	"iter"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseFields(t *testing.T) {
	tests := []struct {
		name   string
		record string
		want   []string
	}{
		{name: "empty record", record: "", want: []string{""}},
		{name: "plain fields", record: "one,two,three", want: []string{"one", "two", "three"}},
		{name: "empty fields", record: ",middle,", want: []string{"", "middle", ""}},
		{name: "quoted comma", record: `one,"two, too",three`, want: []string{"one", "two, too", "three"}},
		{name: "escaped quote", record: `"say ""hello"""`, want: []string{`say "hello"`}},
		{name: "multiline quoted field", record: "\"first line\nsecond line\",tail", want: []string{"first line\nsecond line", "tail"}},
		{name: "comma-heavy quoted field", record: `"a,b,c,d",last`, want: []string{"a,b,c,d", "last"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFields(tt.record)
			if err != nil {
				t.Fatalf("ParseFields() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseFields() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseFieldsMalformed(t *testing.T) {
	tests := []struct {
		name   string
		record string
	}{
		{name: "unterminated quoted field", record: `"unterminated`},
		{name: "bare quote", record: `un"quoted`},
		{name: "character after closing quote", record: `"quoted"x`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseFields(tt.record); err == nil {
				t.Fatal("ParseFields() error = nil, want an error")
			}
		})
	}
}

func TestReadRecord(t *testing.T) {
	input := "plain,record\r\n\"quoted,\nmultiline\",value\nlast,record"
	reader := NewReader(bytes.NewReader([]byte(input)))

	var got []string
	for record := range reader.ReadRecord() {
		got = append(got, record)
	}
	if reader.err != nil {
		t.Fatalf("ReadRecord() error = %v", reader.err)
	}

	want := []string{
		"plain,record",
		"\"quoted,\nmultiline\",value",
		"last,record",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadRecord() = %#v, want %#v", got, want)
	}
}

func TestReadRecordEmptyInput(t *testing.T) {
	reader := NewReader(bytes.NewReader(nil))
	var got []string
	for record := range reader.ReadRecord() {
		got = append(got, record)
	}
	if reader.err != nil {
		t.Fatalf("ReadRecord() error = %v", reader.err)
	}
	if len(got) != 0 {
		t.Fatalf("ReadRecord() = %#v, want no records", got)
	}
}

func TestReadRecordUnterminatedQuotedField(t *testing.T) {
	reader := NewReader(bytes.NewReader([]byte(`"unterminated`)))
	for range reader.ReadRecord() {
		t.Fatal("ReadRecord() yielded a record with an unterminated quote")
	}
	if !errors.Is(reader.err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRecord() error = %v, want %v", reader.err, io.ErrUnexpectedEOF)
	}
}

func TestReadRecordStopsWhenYieldReturnsFalse(t *testing.T) {
	reader := NewReader(bytes.NewReader([]byte("one\ntwo\n")))
	count := 0
	for range reader.ReadRecord() {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("yield count = %d, want 1", count)
	}
	if reader.err != nil {
		t.Fatalf("ReadRecord() error = %v, want nil", reader.err)
	}
}

func TestStructReaderMapsJSONTagsAndConvertsValues(t *testing.T) {
	type row struct {
		Name   string  `json:"display_name"`
		Count  int     `json:"count"`
		Active bool    `json:"active"`
		Score  float64 `json:"score"`
		Ignore string  `json:"-"`
	}

	input := "active,unknown,display_name,score,count\ntrue,ignored,Ada,9.5,12\n"
	records := NewReader(bytes.NewReader([]byte(input)))
	reader, err := NewStructReader[row](records)
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	if reader.records != records {
		t.Fatal("NewStructReader() did not reuse the supplied RecordReader")
	}

	var got row
	count := 0
	for value := range reader.Read() {
		got = value
		count++
	}
	if count != 1 {
		t.Fatalf("Read() yielded %d rows, want 1", count)
	}
	if reader.err != nil {
		t.Fatalf("Read() error = %v, want nil", reader.err)
	}
	want := row{Name: "Ada", Count: 12, Active: true, Score: 9.5}
	if got != want {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}

	count = 0
	for range reader.Read() {
		count++
	}
	if count != 0 {
		t.Fatalf("Read() after final row yielded %d rows, want 0", count)
	}
}

func TestStructReaderSupportsTextUnmarshalerAndPointers(t *testing.T) {
	type row struct {
		Number *int `json:"number"`
	}
	input := "number\n42\n"
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte(input))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	var got row
	for value := range reader.Read() {
		got = value
	}
	if reader.err != nil {
		t.Fatalf("Read() error = %v, want nil", reader.err)
	}
	if got.Number == nil || *got.Number != 42 {
		t.Fatalf("Read().Number = %v, want pointer to 42", got.Number)
	}
}

func TestStructReaderLeavesMissingFieldsZeroAndEmptyPointersNil(t *testing.T) {
	type row struct {
		Name  string  `json:"name"`
		Count int     `json:"count"`
		Note  *string `json:"note"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("name,note\nAda,\n"))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	var got row
	for value := range reader.Read() {
		got = value
	}
	if reader.err != nil {
		t.Fatalf("Read() error = %v, want nil", reader.err)
	}
	if got.Name != "Ada" || got.Count != 0 || got.Note != nil {
		t.Fatalf("Read() = %#v, want name Ada, zero count, nil note", got)
	}
}

func TestNewStructReaderRequiresHeader(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	if _, err := NewStructReader[row](NewReader(bytes.NewReader(nil))); !errors.Is(err, io.EOF) {
		t.Fatalf("NewStructReader() error = %v, want EOF", err)
	}
}

func TestStructReaderReportsRowConversionErrors(t *testing.T) {
	type row struct {
		Count int `json:"count"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("count\nnot-an-int\nnext\n"))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	for range reader.Read() {
		t.Fatal("Read() yielded a row with an invalid integer")
	}
	if reader.err == nil {
		t.Fatal("Read() error = nil, want conversion error")
	}
}

func TestStructReaderRejectsDuplicateHeader(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	_, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("name,name\nAda,Grace\n"))))
	if err == nil {
		t.Fatal("NewStructReader() error = nil, want duplicate header error")
	}
}

func TestStructReaderRejectsNonStructType(t *testing.T) {
	_, err := NewStructReader[string](NewReader(bytes.NewReader([]byte("name\nAda\n"))))
	if err == nil {
		t.Fatal("NewStructReader() error = nil, want non-struct type error")
	}
}

func TestStructReaderRejectsRowWidthMismatch(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("name\nAda,extra\n"))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	for range reader.Read() {
		t.Fatal("Read() yielded a row with mismatched width")
	}
	if reader.err == nil {
		t.Fatal("Read() error = nil, want row width error")
	}
}

func TestStructReaderRejectsUnsupportedFieldType(t *testing.T) {
	type row struct {
		Values []string `json:"values"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("values\none\n"))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	for range reader.Read() {
		t.Fatal("Read() yielded a row with unsupported field type")
	}
	if reader.err == nil {
		t.Fatal("Read() error = nil, want unsupported field type error")
	}
}

type testTextValue string

func (v *testTextValue) UnmarshalText(text []byte) error {
	*v = testTextValue(strings.ToUpper(string(text)))
	return nil
}

func TestStructReaderUsesTextUnmarshaler(t *testing.T) {
	type row struct {
		Value testTextValue `json:"value"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte("value\nhello\n"))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}
	var got row
	for value := range reader.Read() {
		got = value
	}
	if reader.err != nil {
		t.Fatalf("Read() error = %v, want nil", reader.err)
	}
	if got.Value != "HELLO" {
		t.Fatalf("Read().Value = %q, want HELLO", got.Value)
	}
}

func TestStructColumnsMapsFieldNamesAndJSONTags(t *testing.T) {
	type row struct {
		Name  string `json:"display_name"`
		Count int    `json:"count"`
	}

	columns, err := structColumns[row](map[string]int{"count": 0, "display_name": 1})
	if err != nil {
		t.Fatalf("structColumns() error = %v", err)
	}
	want := map[int]int{1: 0, 0: 1}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("structColumns() = %#v, want %#v", columns, want)
	}
}

func TestDecodeStructRowUsesStructAndColumnIndexes(t *testing.T) {
	type row struct {
		Name  string `json:"display_name"`
		Count int    `json:"count"`
	}
	got, err := decodeStructRow[row](map[int]int{1: 0, 0: 1}, []string{"7", "Ada"})
	if err != nil {
		t.Fatalf("decodeStructRow() error = %v", err)
	}
	want := row{Name: "Ada", Count: 7}
	if got != want {
		t.Fatalf("decodeStructRow() = %#v, want %#v", got, want)
	}
}

func TestStructReaderParsesRFC3339Time(t *testing.T) {
	type row struct {
		CreatedAt time.Time `json:"created_at"`
	}
	reader, err := NewStructReader[row](NewReader(bytes.NewReader([]byte(
		"created_at\n2026-10-10T16:48:27+08:00\n",
	))))
	if err != nil {
		t.Fatalf("NewStructReader() error = %v", err)
	}

	var got row
	for value := range reader.Read() {
		got = value
	}
	if reader.err != nil {
		t.Fatalf("Read() error = %v, want nil", reader.err)
	}
	want := time.Date(2026, time.October, 10, 16, 48, 27, 0, time.FixedZone("", 8*60*60))
	if !got.CreatedAt.Equal(want) {
		t.Fatalf("Read().CreatedAt = %s, want %s", got.CreatedAt, want)
	}
}

func TestCollectByIndexMapsStructFieldsToColumnIndexes(t *testing.T) {
	type row struct {
		Name  string `json:"display_name"`
		Count int    `json:"count"`
	}
	rows := iter.Seq[[]string](func(yield func([]string) bool) {
		yield([]string{"7", "Ada"})
	})
	var got []row
	for value, _ := range CollectByIndex[row](map[string]int{"Count": 0, "Name": 1}, rows) {
		got = append(got, value)
	}
	want := []row{{Name: "Ada", Count: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectByIndex() = %#v, want %#v", got, want)
	}
}
