package schema

import "testing"

func TestSMSForwardDestination(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"off", true}, {"+4915112345678", true}, {"+12345678", true},
		{"", false}, {"123456789", false}, {"+012345678", false}, {"+1234567", false},
		{"+1234567890123456", false}, {"+49123 45678", false}, {"+49123x5678", false},
	} {
		err := ValidateFormat("sms-forward-destination", tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("%q: %v", tc.value, err)
		}
	}
}
