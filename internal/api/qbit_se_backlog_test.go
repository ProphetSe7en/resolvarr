package api

import (
	"reflect"
	"testing"
)

func TestSplitQbitTags(t *testing.T) {
	cases := map[string][]string{
		"":                  nil,
		"   ":               nil,
		"a":                 {"a"},
		"a,b,c":             {"a", "b", "c"},
		" a , b , c ":       {"a", "b", "c"},
		"a,,b":              {"a", "b"},
		"S01,S01E05,custom": {"S01", "S01E05", "custom"},
	}
	for in, want := range cases {
		got := splitQbitTags(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("splitQbitTags(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestHasAllTags(t *testing.T) {
	cases := []struct {
		name    string
		current []string
		wanted  []string
		want    bool
	}{
		{"both present, exact case",
			[]string{"S01", "S01E05"}, []string{"S01", "S01E05"}, true},
		{"both present, mixed case",
			[]string{"s01", "S01e05"}, []string{"S01", "s01E05"}, true},
		{"current has extra tags — still all wanted present",
			[]string{"S01", "S01E05", "favorite"}, []string{"S01"}, true},
		{"missing one",
			[]string{"S01"}, []string{"S01", "S01E05"}, false},
		{"current empty",
			nil, []string{"S01"}, false},
		{"wanted empty — vacuously true",
			[]string{"x"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasAllTags(c.current, c.wanted); got != c.want {
				t.Errorf("hasAllTags(%v, %v) = %v, want %v", c.current, c.wanted, got, c.want)
			}
		})
	}
}
