package reader

import (
	"fmt"
	"io"
	"iter"
	"strings"
)

type RecordReader struct {
	reader       io.ReaderAt
	offset       int64
	recordBuffer []byte
	err          error
}

func NewReader(r io.ReaderAt) *RecordReader {
	return &RecordReader{
		reader: r,
	}
}

// readRecord yields raw CSV records until EOF.
func (r *RecordReader) ReadRecord() iter.Seq[string] {
	return func(yield func(string) bool) {
		r.err = nil
		for {
			record, err := r.readRecord()
			if err == io.EOF {
				return
			}
			if err != nil {
				r.err = err
				return
			}
			if !yield(record) {
				return
			}
		}
	}
}

func (r *RecordReader) readRecord() (string, error) {
	r.recordBuffer = r.recordBuffer[:0]
	inQuotes := false
	atFieldStart := true

	for {
		b, readErr := r.readByte()
		if readErr != nil {
			if readErr != io.EOF || len(r.recordBuffer) == 0 {
				return "", readErr
			}
			if inQuotes {
				return "", io.ErrUnexpectedEOF
			}
			return string(r.recordBuffer), nil
		}

		if inQuotes {
			r.recordBuffer = append(r.recordBuffer, b)
			if b == '"' {
				next, peekErr := r.peekByte()
				if peekErr == nil && next == '"' {
					escapedQuote, err := r.readByte()
					if err != nil {
						return "", err
					}
					r.recordBuffer = append(r.recordBuffer, escapedQuote)
				} else if peekErr == io.EOF {
					inQuotes = false
				} else if peekErr != nil {
					return "", peekErr
				} else {
					inQuotes = false
				}
			}
			continue
		}

		switch b {
		case '"':
			r.recordBuffer = append(r.recordBuffer, b)
			if atFieldStart {
				inQuotes = true
			}
			atFieldStart = false
		case ',':
			r.recordBuffer = append(r.recordBuffer, b)
			atFieldStart = true
		case '\n':
			r.recordBuffer = append(r.recordBuffer, b)
			return string(r.recordBuffer[:len(r.recordBuffer)-1]), nil
		case '\r':
			r.recordBuffer = append(r.recordBuffer, b)
			end := len(r.recordBuffer) - 1
			if next, peekErr := r.peekByte(); peekErr == nil {
				if next == '\n' {
					lineFeed, err := r.readByte()
					if err != nil {
						return "", err
					}
					r.recordBuffer = append(r.recordBuffer, lineFeed)
				}
			} else if peekErr != io.EOF {
				return "", peekErr
			}
			return string(r.recordBuffer[:end]), nil
		default:
			r.recordBuffer = append(r.recordBuffer, b)
			atFieldStart = false
		}
	}
}

func (r *RecordReader) readByte() (byte, error) {
	b, err := r.peekByte()
	if err != nil {
		return 0, err
	}
	r.offset++
	return b, nil
}

func (r *RecordReader) peekByte() (byte, error) {
	var b [1]byte
	n, err := r.reader.ReadAt(b[:], r.offset)
	if n == 1 {
		return b[0], nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return 0, err
}

func ParseFields(record string) ([]string, error) {
	fields := make([]string, 0, strings.Count(record, ",")+1)
	for i := 0; ; {
		var field strings.Builder
		if i < len(record) && record[i] == '"' {
			i++
			closed := false
			for i < len(record) {
				if record[i] != '"' {
					field.WriteByte(record[i])
					i++
					continue
				}
				if i+1 < len(record) && record[i+1] == '"' {
					field.WriteByte('"')
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			if !closed {
				return nil, io.ErrUnexpectedEOF
			}
			if i < len(record) && record[i] != ',' {
				return nil, fmt.Errorf("unexpected character %q after closing quote at byte %d", record[i], i)
			}
		} else {
			start := i
			for i < len(record) && record[i] != ',' {
				if record[i] == '"' {
					return nil, fmt.Errorf("bare quote in unquoted field at byte %d", i)
				}
				i++
			}
			field.WriteString(record[start:i])
		}
		fields = append(fields, field.String())
		if i == len(record) {
			return fields, nil
		}
		i++
		if i == len(record) {
			fields = append(fields, "")
			return fields, nil
		}
	}
}
