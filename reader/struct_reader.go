package reader

import (
	"encoding"
	"fmt"
	"io"
	"iter"
	"reflect"
	"strconv"
	"strings"
)

type StructReader[T any] struct {
	records     *RecordReader
	columns     map[int]int
	headerWidth int
	err         error
	rowNumber   int
}

type StructSeqReader[T any] struct {
	columns map[int]int
	rows    iter.Seq[[]string]
	width   int
	err     error
}

var textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

// NewStructReader reads the CSV header and maps its column names to T's fields.
func NewStructReader[T any](records *RecordReader) (*StructReader[T], error) {
	if records == nil {
		return nil, fmt.Errorf("csv reader: record reader must not be nil")
	}

	reader := &StructReader[T]{records: records}
	headerRecord, err := reader.records.readRecord()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("csv reader: missing header: %w", err)
		}
		return nil, fmt.Errorf("csv reader: read header: %w", err)
	}
	headers, err := ParseFields(headerRecord)
	if err != nil {
		return nil, fmt.Errorf("csv reader: parse header: %w", err)
	}

	headerIndexes := make(map[string]int, len(headers))
	for i, header := range headers {
		if previous, exists := headerIndexes[header]; exists {
			return nil, fmt.Errorf("csv reader: duplicate header %q at columns %d and %d", header, previous+1, i+1)
		}
		headerIndexes[header] = i
	}

	reader.columns, err = structColumns[T](headerIndexes)
	if err != nil {
		return nil, err
	}
	reader.headerWidth = len(headers)
	return reader, nil
}

// Read returns the next decoded CSV row. It returns io.EOF after the final row.
func (r *StructReader[T]) Read() iter.Seq[T] {
	return func(yield func(T) bool) {
		for record := range r.records.ReadRecord() {
			r.rowNumber++
			values, err := ParseFields(record)
			if err != nil {
				r.err = fmt.Errorf("csv reader: parse row %d: %w", r.rowNumber, err)
				return
			}
			if len(values) != r.headerWidth {
				r.err = fmt.Errorf("csv reader: row %d has %d fields; header has %d", r.rowNumber, len(values), r.headerWidth)
				return
			}
			value, err := decodeStructRow[T](r.columns, values)
			if err != nil {
				r.err = fmt.Errorf("csv reader: row %d: %w", r.rowNumber, err)
				return
			}

			if !yield(value) {
				return
			}
		}
	}
}

// CollectByIndex maps Go struct field names or JSON tags to indexes in each row.
func CollectByIndex[T any](columns map[string]int, rows iter.Seq[[]string]) iter.Seq2[T, []string] {
	fieldIndexes, err := structFieldIndexes[T]()
	if err != nil {
		return nil
	}
	indexedColumns := make(map[int]int, len(columns))
	for name, column := range columns {
		if fieldIndex, ok := fieldIndexes[name]; ok {
			indexedColumns[fieldIndex] = column
		}
	}
	return func(yield func(T, []string) bool) {
		for fields := range rows {
			value, err := decodeStructRow[T](indexedColumns, fields)
			if err != nil {
				panic(err)
			}
			if !yield(value, fields) {
				return
			}
		}
	}
}

func decodeStructRow[T any](columns map[int]int, fields []string) (T, error) {
	var value T
	typeOfT := reflect.TypeOf((*T)(nil)).Elem()
	if typeOfT.Kind() != reflect.Struct {
		return value, fmt.Errorf("csv reader: T must be a struct, got %v", typeOfT)
	}
	structValue := reflect.ValueOf(&value).Elem()
	for fieldIndex, column := range columns {
		if fieldIndex < 0 || fieldIndex >= typeOfT.NumField() || typeOfT.Field(fieldIndex).PkgPath != "" {
			return value, fmt.Errorf("field index %d is not an exported field on T", fieldIndex)
		}
		name := typeOfT.Field(fieldIndex).Name
		if column < 0 || column >= len(fields) {
			return value, fmt.Errorf("column index %d for field %q is outside row width %d", column, name, len(fields))
		}
		field := structValue.Field(fieldIndex)
		if err := setStructField(field, fields[column]); err != nil {
			return value, fmt.Errorf("column %q: %w", name, err)
		}
	}
	return value, nil
}

func structColumns[T any](headerIndexes map[string]int) (map[int]int, error) {
	fieldIndexes, err := structFieldHeaders[T]()
	if err != nil {
		return nil, err
	}

	typeOfT := reflect.TypeOf((*T)(nil)).Elem()
	columns := make(map[int]int)
	for fieldName, header := range fieldIndexes {
		index, ok := headerIndexes[header]
		if !ok {
			continue
		}
		fieldIndex, ok := typeOfT.FieldByName(fieldName)
		if !ok {
			return nil, fmt.Errorf("csv reader: field %q not found on T", fieldName)
		}
		columns[fieldIndex.Index[0]] = index
	}
	return columns, nil
}

func structFieldHeaders[T any]() (map[string]string, error) {
	if _, err := structFieldIndexes[T](); err != nil {
		return nil, err
	}

	typeOfT := reflect.TypeOf((*T)(nil)).Elem()
	headers := make(map[string]string, typeOfT.NumField())
	for i := 0; i < typeOfT.NumField(); i++ {
		field := typeOfT.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tagName := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if tagName == "-" {
			continue
		}
		if tagName == "" {
			tagName = field.Name
		}
		headers[field.Name] = tagName
	}
	return headers, nil
}

func structFieldIndexes[T any]() (map[string]int, error) {
	typeOfT := reflect.TypeOf((*T)(nil)).Elem()
	if typeOfT.Kind() != reflect.Struct {
		return nil, fmt.Errorf("csv reader: T must be a struct, got %v", typeOfT)
	}

	indexes := make(map[string]int, typeOfT.NumField()*2)
	for i := 0; i < typeOfT.NumField(); i++ {
		field := typeOfT.Field(i)
		if field.PkgPath != "" {
			continue
		}

		if previous, exists := indexes[field.Name]; exists && previous != i {
			return nil, fmt.Errorf("csv reader: multiple struct fields use name %q", field.Name)
		}
		indexes[field.Name] = i

		tagName := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if tagName != "" && tagName != "-" {
			if previous, exists := indexes[tagName]; exists && previous != i {
				return nil, fmt.Errorf("csv reader: multiple struct fields use name %q", tagName)
			}
			indexes[tagName] = i
		}
	}
	return indexes, nil
}

func setStructField(field reflect.Value, text string) error {
	if field.Kind() == reflect.Pointer {
		if text == "" {
			field.SetZero()
			return nil
		}
		field.Set(reflect.New(field.Type().Elem()))
		return setStructField(field.Elem(), text)
	}

	if field.CanAddr() && field.Addr().Type().Implements(textUnmarshalerType) {
		return field.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(text))
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(text)
	case reflect.Bool:
		value, err := strconv.ParseBool(text)
		if err != nil {
			return err
		}
		field.SetBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := strconv.ParseInt(text, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := strconv.ParseUint(text, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(value)
	case reflect.Float32, reflect.Float64:
		value, err := strconv.ParseFloat(text, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(value)
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
	return nil
}
