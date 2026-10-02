package pagination

import (
	"errors"
	"testing"
)

var testBounds = Bounds{DefaultLimit: 20, MaxLimit: 100}

func TestParseStrictDefaults(t *testing.T) {
	limit, offset, err := ParseStrict("", "", testBounds)
	if err != nil {
		t.Fatalf("ParseStrict() error = %v", err)
	}
	if limit != testBounds.DefaultLimit || offset != 0 {
		t.Fatalf("ParseStrict() = (%d, %d), want (%d, 0)", limit, offset, testBounds.DefaultLimit)
	}
}

func TestParseStrictAcceptsBoundaries(t *testing.T) {
	testCases := []struct {
		name       string
		rawLimit   string
		rawOffset  string
		wantLimit  int
		wantOffset int
	}{
		{name: "smallest limit", rawLimit: "1", rawOffset: "0", wantLimit: 1, wantOffset: 0},
		{name: "limit equal to max", rawLimit: "100", rawOffset: "40", wantLimit: 100, wantOffset: 40},
		{name: "limit above max is capped", rawLimit: "101", rawOffset: "", wantLimit: 100, wantOffset: 0},
		{name: "huge limit is capped", rawLimit: "999999", rawOffset: "", wantLimit: 100, wantOffset: 0},
		{name: "surrounding whitespace", rawLimit: " 25 ", rawOffset: " 50 ", wantLimit: 25, wantOffset: 50},
		{name: "offset past any page", rawLimit: "20", rawOffset: "1000000", wantLimit: 20, wantOffset: 1000000},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			limit, offset, err := ParseStrict(testCase.rawLimit, testCase.rawOffset, testBounds)
			if err != nil {
				t.Fatalf("ParseStrict() error = %v", err)
			}
			if limit != testCase.wantLimit || offset != testCase.wantOffset {
				t.Fatalf("ParseStrict() = (%d, %d), want (%d, %d)", limit, offset, testCase.wantLimit, testCase.wantOffset)
			}
		})
	}
}

func TestParseStrictRejectsInvalidValues(t *testing.T) {
	testCases := []struct {
		name      string
		rawLimit  string
		rawOffset string
		wantErr   error
	}{
		{name: "zero limit", rawLimit: "0", wantErr: ErrInvalidLimit},
		{name: "negative limit", rawLimit: "-5", wantErr: ErrInvalidLimit},
		{name: "non numeric limit", rawLimit: "abc", wantErr: ErrInvalidLimit},
		{name: "decimal limit", rawLimit: "2.5", wantErr: ErrInvalidLimit},
		{name: "overflowing limit", rawLimit: "99999999999999999999", wantErr: ErrInvalidLimit},
		{name: "negative offset", rawOffset: "-1", wantErr: ErrInvalidOffset},
		{name: "non numeric offset", rawOffset: "first", wantErr: ErrInvalidOffset},
		{name: "overflowing offset", rawOffset: "99999999999999999999", wantErr: ErrInvalidOffset},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := ParseStrict(testCase.rawLimit, testCase.rawOffset, testBounds)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("ParseStrict() error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestParseStrictRejectsMisconfiguredBounds(t *testing.T) {
	testCases := []struct {
		name   string
		bounds Bounds
	}{
		{name: "zero default", bounds: Bounds{DefaultLimit: 0, MaxLimit: 10}},
		{name: "default above max", bounds: Bounds{DefaultLimit: 50, MaxLimit: 10}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := ParseStrict("", "", testCase.bounds)
			if !errors.Is(err, ErrInvalidBounds) {
				t.Fatalf("ParseStrict() error = %v, want %v", err, ErrInvalidBounds)
			}
		})
	}
}
