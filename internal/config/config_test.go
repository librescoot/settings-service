package config

import (
	"reflect"
	"testing"
)

func TestParseRedisSettings(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected *Config
	}{
		{
			name: "parse standard sections",
			input: map[string]string{
				"scooter.auto-standby-seconds": "300",
				"scooter.brake-hibernation":    "true",
				"cellular.apn":                 "internet.lebara.de",
				"updates.channel":              "stable",
			},
			expected: &Config{
				Fields: map[string]interface{}{},
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"auto-standby-seconds": "300",
						"brake-hibernation":    "true",
					},
					"cellular": {
						"apn": "internet.lebara.de",
					},
					"updates": {
						"channel": "stable",
					},
				},
			},
		},
		{
			name: "parse arbitrary section names",
			input: map[string]string{
				"engine-ecu.mode":    "normal",
				"engine-ecu.torque":  "100",
				"alarm.enabled":      "true",
			},
			expected: &Config{
				Fields: map[string]interface{}{},
				Sections: map[string]map[string]interface{}{
					"engine-ecu": {
						"mode":   "normal",
						"torque": "100",
					},
					"alarm": {
						"enabled": "true",
					},
				},
			},
		},
		{
			name: "top-level keys without dots",
			input: map[string]string{
				"scooter.brake-hibernation": "true",
				"version":                   "1",
				"cellular.apn":              "internet.lebara.de",
			},
			expected: &Config{
				Fields: map[string]interface{}{
					"version": "1",
				},
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"brake-hibernation": "true",
					},
					"cellular": {
						"apn": "internet.lebara.de",
					},
				},
			},
		},
		{
			name: "keys with multiple dots go into section with remainder as key",
			input: map[string]string{
				"scooter.nested.setting": "value",
			},
			expected: &Config{
				Fields: map[string]interface{}{},
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"nested.setting": "value",
					},
				},
			},
		},
		{
			name:  "empty input",
			input: map[string]string{},
			expected: &Config{
				Fields:   map[string]interface{}{},
				Sections: map[string]map[string]interface{}{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRedisSettings(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ParseRedisSettings() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestToRedisFields(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected map[string]interface{}
	}{
		{
			name: "convert sections",
			config: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"auto-standby-seconds": "300",
						"enable-horn":          "true",
					},
					"cellular": {
						"apn": "internet.lebara.de",
					},
				},
			},
			expected: map[string]interface{}{
				"scooter.auto-standby-seconds": "300",
				"scooter.enable-horn":          "true",
				"cellular.apn":                 "internet.lebara.de",
			},
		},
		{
			name: "convert top-level fields",
			config: &Config{
				Fields: map[string]interface{}{
					"version": "1",
				},
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"brake-hibernation": "true",
					},
				},
			},
			expected: map[string]interface{}{
				"version":                   "1",
				"scooter.brake-hibernation": "true",
			},
		},
		{
			name: "empty config",
			config: &Config{
				Sections: map[string]map[string]interface{}{},
			},
			expected: map[string]interface{}{},
		},
		{
			name: "keys with dots in section key name",
			config: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"nested.setting": "value",
					},
				},
			},
			expected: map[string]interface{}{
				"scooter.nested.setting": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ToRedisFields()
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ToRedisFields() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseRedisSettingsRoundTrip(t *testing.T) {
	input := map[string]string{
		"scooter.auto-standby-seconds": "300",
		"scooter.brake-hibernation":    "true",
		"cellular.apn":                 "internet.lebara.de",
		"engine-ecu.mode":              "normal",
		"version":                      "1",
	}

	config := ParseRedisSettings(input)
	output := config.ToRedisFields()

	outputStr := make(map[string]string)
	for k, v := range output {
		outputStr[k] = v.(string)
	}

	if !reflect.DeepEqual(input, outputStr) {
		t.Errorf("round-trip failed.\n  input:  %v\n  output: %v", input, outputStr)
	}
}
