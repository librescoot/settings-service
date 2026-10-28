package schema

import "fmt"

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
	return nil
}
