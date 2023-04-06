// aviation_test.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"testing"
)

func TestFrequencyFormat(t *testing.T) {
	type FS struct {
		f Frequency
		s string
	}

	for _, fs := range []FS{FS{f: Frequency(121900), s: "121.900"},
		FS{f: Frequency(130050), s: "130.050"},
		FS{f: Frequency(128000), s: "128.000"},
	} {
		if fs.f.String() != fs.s {
			t.Errorf("Frequency String() %q; expected %q", fs.f.String(), fs.s)
		}
	}
}

func TestParseSquawk(t *testing.T) {
	for _, squawk := range []string{"11111", "7778", "0801", "9000"} {
		if _, err := ParseSquawk(squawk); err == nil {
			t.Errorf("Expected error return value for invalid squawk %q", squawk)
		}
	}

	for _, squawk := range []string{"0601", "3700", "7777", "0000", "1724"} {
		if ps, err := ParseSquawk(squawk); err != nil {
			t.Errorf("%v: Unexpected error return value for valid squawk %q", err, squawk)
		} else if ps.String() != squawk {
			t.Errorf("Parsing squawk %s doesn't give match from String(): %s", squawk, ps.String())
		}
	}
}

func TestRECATDistances(t *testing.T) {
	if dist, err := RECATDistance("A", "C"); err != nil {
		t.Errorf("unexpected RECAT error %v", err)
	} else if dist != 5 {
		t.Errorf("RECAT got %d expected 5", dist)
	}

	if dist, err := RECATDistance("B", "D"); err != nil {
		t.Errorf("unexpected RECAT error %v", err)
	} else if dist != 4 {
		t.Errorf("RECAT got %d expected 4", dist)
	}

	if dist, err := RECATDistance("B", "F"); err != nil {
		t.Errorf("unexpected RECAT error %v", err)
	} else if dist != 7 {
		t.Errorf("RECAT got %d expected 7", dist)
	}

	if _, err := RECATDistance("G", "A"); err == nil {
		lg.Errorf("did not get error for invalid RECAT category")
	}
}
