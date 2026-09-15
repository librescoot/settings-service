package schema

import "testing"

func TestValidateTripExpungeBoundaryCorpus(t *testing.T) {
	valid := []string{
		"never",
		"age:1ns",
		"age:1us",
		"age:1.5ms",
		"age:0.5s",
		"age:1h30m",
		"age:1d",
		"age:106751d",
		"count:0",
		"count:9223372036854775807",
		"size:0",
		"size:9223372036854775807",
	}
	for _, value := range valid {
		t.Run(value, func(t *testing.T) {
			if err := ValidateTripExpunge(value); err != nil {
				t.Errorf("ValidateTripExpunge(%q) error: %v", value, err)
			}
		})
	}

	invalid := []string{
		"age:0",
		"age:0ns",
		"age:0.5ns",
		"age:.5s",
		"age:01h",
		"age:00.5s",
		"age:1.s",
		"age:1µs",
		"age:1μs",
		"age:106752d",
		"age:2562047h47m16.854775808s",
		"count:01",
		"count:+1",
		"count:9223372036854775808",
		"size:-1",
		"size:9223372036854775808",
		" age:1s",
		"age:1s ",
		"age:1 s",
		"count: 1",
		"size:1\t",
	}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			if err := ValidateTripExpunge(value); err == nil {
				t.Errorf("ValidateTripExpunge(%q) succeeded, want error", value)
			}
		})
	}
}

func TestValidateTripExpungeRejectsMalformedDuration(t *testing.T) {
	for _, value := range []string{
		"",
		"never ",
		"age:",
		"age:00d",
		"age:1d2h",
		"age:+1h",
		"age:-1h",
		"age:1w",
		"age:1h:extra",
		"count:1.0",
		"size:01",
		"unknown:1",
	} {
		t.Run(value, func(t *testing.T) {
			if err := ValidateTripExpunge(value); err == nil {
				t.Errorf("ValidateTripExpunge(%q) succeeded, want error", value)
			}
		})
	}
}

func TestValidateFormat(t *testing.T) {
	if err := ValidateFormat(TripExpungeFormat, "age:365d"); err != nil {
		t.Fatalf("ValidateFormat() error: %v", err)
	}
	if err := ValidateFormat(TripExpungeFormat, "age:0d"); err == nil {
		t.Error("ValidateFormat() accepted invalid trip expunge value")
	}
	if err := ValidateFormat("unknown", "value"); err == nil {
		t.Error("ValidateFormat() accepted an unknown format")
	}
}
