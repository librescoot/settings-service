package schema

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	TripExpungeFormat  = "trip-expunge"
	maxTripExpungeDays = int64(time.Duration(1<<63-1) / (24 * time.Hour))
)

// ValidateFormat validates a value using a schema string format.
func ValidateFormat(format, value string) error {
	switch format {
	case "":
		return nil
	case TripExpungeFormat:
		return ValidateTripExpunge(value)
	default:
		return fmt.Errorf("unsupported setting format %q", format)
	}
}

// ValidateTripExpunge validates the atomic trip-history expunge policy.
func ValidateTripExpunge(value string) error {
	if value == "never" {
		return nil
	}

	policy, argument, ok := strings.Cut(value, ":")
	if !ok || argument == "" {
		return fmt.Errorf("trip expunge must be never, age:<duration>, count:<trips>, or size:<bytes>")
	}

	switch policy {
	case "age":
		return validateTripExpungeAge(argument)
	case "count", "size":
		return validateNonnegativeInt64(argument)
	default:
		return fmt.Errorf("unknown trip expunge policy %q", policy)
	}
}

func validateTripExpungeAge(value string) error {
	if strings.HasSuffix(value, "d") {
		days := strings.TrimSuffix(value, "d")
		if !isCanonicalPositiveDecimal(days) {
			return fmt.Errorf("trip expunge age in days must be a positive integer")
		}
		parsedDays, err := strconv.ParseInt(days, 10, 64)
		if err != nil || parsedDays > maxTripExpungeDays {
			return fmt.Errorf("trip expunge age duration overflows")
		}
		return nil
	}

	if !isASCIIGoDuration(value) {
		return fmt.Errorf("trip expunge age must use ASCII Go duration units ns, us, ms, s, m, or h")
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fmt.Errorf("trip expunge age must be between 1ns and the largest int64 duration")
	}
	return nil
}

// isASCIIGoDuration accepts the transport-safe subset of Go duration syntax:
// every component has a canonical integer part and an optional non-empty fraction.
// Days are handled separately.
func isASCIIGoDuration(value string) bool {
	for pos := 0; pos < len(value); {
		start := pos
		for pos < len(value) && value[pos] >= '0' && value[pos] <= '9' {
			pos++
		}
		integerEnd := pos
		if integerEnd == start || (integerEnd-start > 1 && value[start] == '0') {
			return false
		}
		if pos < len(value) && value[pos] == '.' {
			pos++
			fractionStart := pos
			for pos < len(value) && value[pos] >= '0' && value[pos] <= '9' {
				pos++
			}
			if pos == fractionStart {
				return false
			}
		}

		switch {
		case strings.HasPrefix(value[pos:], "ns"), strings.HasPrefix(value[pos:], "us"), strings.HasPrefix(value[pos:], "ms"):
			pos += 2
		case pos < len(value) && (value[pos] == 's' || value[pos] == 'm' || value[pos] == 'h'):
			pos++
		default:
			return false
		}
	}
	return true
}

func validateNonnegativeInt64(value string) error {
	if !isCanonicalNonnegativeDecimal(value) {
		return fmt.Errorf("trip expunge count and size must be canonical nonnegative decimal integers")
	}
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return fmt.Errorf("trip expunge count and size must fit int64")
	}
	return nil
}

func isCanonicalPositiveDecimal(value string) bool {
	return value != "" && value[0] != '0' && isCanonicalNonnegativeDecimal(value)
}

func isCanonicalNonnegativeDecimal(value string) bool {
	if value == "" {
		return false
	}
	if value == "0" {
		return true
	}
	if value[0] == '0' {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
