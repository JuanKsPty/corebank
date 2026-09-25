package statement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// TestRealFiles parses every file in STATEMENT_DIR and prints what it read.
//
// The fixtures elsewhere in this package are invented; the owner's own exports
// are the only way to confirm the parsers against the real formats, and they
// must never be committed. Run it with:
//
//	STATEMENT_DIR=~/estados go test ./internal/statement -run TestRealFiles -v
//
// Skipped when STATEMENT_DIR is unset.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("STATEMENT_DIR")
	if dir == "" {
		t.Skip("STATEMENT_DIR is not set")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			file, err := Parse(e.Name(), data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			for _, s := range NewFileView(file).Statements {
				t.Logf("%s %s (%s) %s..%s lines=%d opening=%v closing=%v in=%s out=%s",
					s.Institution, s.ExternalNumber, s.Class, s.PeriodStart, s.PeriodEnd, s.LineCount,
					formatted(s.Opening), formatted(s.Closing), s.MoneyIn.Formatted, s.MoneyOut.Formatted)
				for _, w := range s.Warnings {
					t.Logf("  warning: %s", w)
				}
			}
		})
	}
}

func formatted(a *money.Amount) string {
	if a == nil {
		return "—"
	}
	return a.Formatted
}
