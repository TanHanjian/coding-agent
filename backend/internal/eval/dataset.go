package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func LoadCases(r io.Reader) ([]EvalCase, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 16*1024), 2*1024*1024)
	var cases []EvalCase
	seen := map[string]struct{}{}
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var c EvalCase
		decoder := json.NewDecoder(bytesReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			return nil, fmt.Errorf("line %d: invalid JSON: %w", line, err)
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if _, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("line %d: duplicate case id %q", line, c.ID)
		}
		seen[c.ID] = struct{}{}
		cases = append(cases, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}
	return cases, nil
}

func LoadCasesFile(path string) ([]EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadCases(f)
}

// bytesReader avoids exposing a mutable bytes.Buffer to the decoder while
// keeping the per-line parser allocation small.
type byteReader struct {
	data   []byte
	offset int
}

func bytesReader(data []byte) io.Reader { return &byteReader{data: data} }
func (r *byteReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
