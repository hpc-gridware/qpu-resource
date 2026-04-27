// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

package qrmiocs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Record describes one acquired QRMI resource. The wire format on disk is a
// single tab-separated line per record with no header:
//
//	name <TAB> type <TAB> token <TAB> acquired_epoch <NEWLINE>
//
// The TSV format is shared with the C version of the prolog/epilog so that
// either implementation can read what the other writes during migration.
type Record struct {
	Name          string
	Type          int
	Token         string
	AcquiredEpoch int64
}

// WriteAtomic writes records to path via a temp-file-and-rename, so a reader
// (the epilog) never observes a partial file even if the prolog is killed
// mid-write. The C version writes in place and is vulnerable to that race.
func WriteAtomic(path string, records []Record) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "qrmi_ocs_acquired.*.tsv")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	w := bufio.NewWriter(tmp)
	for _, r := range records {
		if strings.ContainsAny(r.Name, "\t\n") || strings.ContainsAny(r.Token, "\t\n") {
			tmp.Close()
			return fmt.Errorf("record contains tab or newline in name or token")
		}
		if _, err := fmt.Fprintf(w, "%s\t%d\t%s\t%d\n", r.Name, r.Type, r.Token, r.AcquiredEpoch); err != nil {
			tmp.Close()
			return fmt.Errorf("write metadata: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close metadata: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename metadata to %s: %w", path, err)
	}
	return nil
}

// ErrNoRecord is returned when the metadata file exists but contains zero
// records. The epilog treats this as an error per the strict single-record
// contract.
var ErrNoRecord = errors.New("metadata file has no records")

// ErrMultipleRecords is returned when the metadata file contains more than
// one record. The epilog treats this as an error per the strict
// single-record contract; multi-record support would require a different
// scheduler model.
var ErrMultipleRecords = errors.New("metadata file has multiple records")

// ReadStrictSingle reads exactly one record from path. Empty lines are
// skipped; zero or more than one non-empty record returns an error so the
// epilog can refuse to release ambiguous state.
func ReadStrictSingle(path string) (Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	var got Record
	have := false
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if have {
			return Record{}, ErrMultipleRecords
		}
		rec, perr := parseRecordLine(line)
		if perr != nil {
			return Record{}, perr
		}
		got = rec
		have = true
	}
	if err := scanner.Err(); err != nil {
		return Record{}, fmt.Errorf("read metadata: %w", err)
	}
	if !have {
		return Record{}, ErrNoRecord
	}
	return got, nil
}

func parseRecordLine(line string) (Record, error) {
	fields := strings.Split(line, "\t")
	if len(fields) < 3 {
		return Record{}, fmt.Errorf("malformed metadata line: %q", line)
	}
	name, typeText, token := fields[0], fields[1], fields[2]
	if name == "" || typeText == "" || token == "" {
		return Record{}, fmt.Errorf("malformed metadata line: %q", line)
	}
	typ, err := strconv.Atoi(typeText)
	if err != nil || typ < 0 {
		return Record{}, fmt.Errorf("invalid type %q in line %q", typeText, line)
	}
	var epoch int64
	if len(fields) >= 4 && fields[3] != "" {
		epoch, err = strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return Record{}, fmt.Errorf("invalid epoch %q in line %q", fields[3], line)
		}
	}
	return Record{Name: name, Type: typ, Token: token, AcquiredEpoch: epoch}, nil
}
