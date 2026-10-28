package schema

import (
	"fmt"
	"github.com/nyaruka/phonenumbers"
)

func validateSMSForwardDestination(value string) error {
	if value == "off" {
		return nil
	}
	if len(value) < 9 || len(value) > 16 || value[0] != '+' || value[1] < '1' || value[1] > '9' {
		return fmt.Errorf("destination must be off or an international number")
	}
	for _, c := range value[2:] {
		if c < '0' || c > '9' {
			return fmt.Errorf("destination must be off or an international number")
		}
	}
	number, err := phonenumbers.Parse(value, "")
	if err != nil || !phonenumbers.IsValidNumber(number) {
		return fmt.Errorf("destination must be a valid standard number")
	}
	switch phonenumbers.GetNumberType(number) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE, phonenumbers.FIXED_LINE_OR_MOBILE:
		return nil
	default:
		return fmt.Errorf("destination must be a standard mobile or landline number")
	}
}
