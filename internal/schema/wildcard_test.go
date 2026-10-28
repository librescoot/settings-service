package schema

import "testing"

func TestLookupIndexedAndWildcard(t *testing.T) {
	s, err := Parse([]byte(`{
  "dashboard.saved.0.latitude":{"type":"float","pattern":"indexed","min":-90,"max":90},
  "one.*.name":{"type":"string","format":"sms-forward-to","default":"off"},
  "one.*.*.value":{"type":"bool"},
  "one.fixed.name":{"type":"bool"},
  "one.fixed.*.value":{"type":"string"}
 }`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, kind string
		exists    bool
	}{
		{"dashboard.saved.42.latitude", "float", true}, {"dashboard.saved.foo.latitude", "", false},
		{"one.test.name", "string", true}, {"one.fixed.name", "bool", true},
		{"one.a.b.value", "bool", true}, {"one.fixed.b.value", "string", true},
		{"one..name", "", false},
	} {
		got, ok := s.Lookup(tc.key)
		if ok != tc.exists || (ok && got.Type != tc.kind) {
			t.Errorf("Lookup(%q) = %#v, %v", tc.key, got, ok)
		}
	}
	if err := s.ValidateTyped("dashboard.saved.42.latitude", "91"); err == nil {
		t.Fatal("indexed value did not use bounds")
	}
	if _, ok := s.Defaults()["one.*.name"]; ok {
		t.Fatal("wildcard default created a literal key")
	}
}
