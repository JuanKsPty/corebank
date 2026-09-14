package categories

import "testing"

func TestParseKind(t *testing.T) {
	tests := []struct {
		input string
		want  Kind
		ok    bool
	}{
		{"income", KindIncome, true},
		{"expense", KindExpense, true},
		{"", "", false},
		{"savings", "", false},
		{"INCOME", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseKind(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("ParseKind(%q) = %v", tc.input, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ParseKind(%q) = %q, want an error", tc.input, got)
			}
			if tc.ok && got != tc.want {
				t.Errorf("ParseKind(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDefaultSeedsAreAllValidKinds(t *testing.T) {
	if len(defaultSeeds) == 0 {
		t.Fatal("defaultSeeds is empty; a new customer would start with nothing to categorise under")
	}
	for _, seed := range defaultSeeds {
		if _, err := ParseKind(string(seed.kind)); err != nil {
			t.Errorf("default seed %q has an invalid kind %q", seed.name, seed.kind)
		}
		if _, err := normaliseName(seed.name); err != nil {
			t.Errorf("default seed %q would be rejected by normaliseName: %v", seed.name, err)
		}
	}
}
