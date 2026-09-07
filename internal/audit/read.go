package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Query selects records out of the log.
//
// The zero value matches everything the log holds.
type Query struct {
	// Since drops records older than this time.
	Since time.Time
	// Outcome keeps only records with this outcome, or all of them when
	// empty.
	Outcome string
}

// Read returns the matching records, oldest first.
//
// Monthly files mean there is no rotation to do: Since becomes file selection.
// A line that does not parse is skipped rather than failing the whole read, so
// one torn line cannot hide the rest of the month.
func (l Logger) Read(q Query) ([]Record, error) {
	if l.dir == "" {
		return nil, nil
	}

	var out []Record

	for _, path := range l.files(q.Since) {
		records, err := readFile(path, q)
		if err != nil {
			return nil, err
		}

		out = append(out, records...)
	}

	return out, nil
}

// files lists the monthly log files that can hold records at or after since,
// oldest first.
func (l Logger) files(since time.Time) []string {
	end := time.Now().UTC()

	start := since.UTC()
	if start.IsZero() {
		start = end.AddDate(-1, 0, 0)
	}

	var paths []string

	month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !month.After(end) {
		path := l.Path(month)
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}

		month = month.AddDate(0, 1, 0)
	}

	return paths
}

func readFile(path string, q Query) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	var out []Record

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, MaxLine), 4*MaxLine)

	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			// a torn line must not hide the rest of the month.
			continue
		}

		if q.Outcome != "" && rec.Outcome != q.Outcome {
			continue
		}

		if !q.Since.IsZero() && rec.Time.Before(q.Since) {
			continue
		}

		out = append(out, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	return out, nil
}
