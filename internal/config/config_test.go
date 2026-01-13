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
				"scooter.speed_limit": "25",
				"scooter.mode":        "eco",
				"cellular.apn":        "internet.provider.com",
				"updates.channel":     "stable",
			},
			expected: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"speed_limit": "25",
						"mode":        "eco",
					},
					"cellular": {
						"apn": "internet.provider.com",
					},
					"updates": {
						"channel": "stable",
					},
				},
			},
		},
		{
			name: "parse custom sections",
			input: map[string]string{
				"custom.setting1":  "value1",
				"another.setting2": "value2",
				"foo.bar":          "baz",
			},
			expected: &Config{
				Sections: map[string]map[string]interface{}{
					"custom": {
						"setting1": "value1",
					},
					"another": {
						"setting2": "value2",
					},
					"foo": {
						"bar": "baz",
					},
				},
			},
		},
		{
			name: "ignore keys without dots",
			input: map[string]string{
				"scooter.speed_limit": "25",
				"invalidkey":          "should_be_ignored",
				"cellular.apn":        "internet.provider.com",
			},
			expected: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"speed_limit": "25",
					},
					"cellular": {
						"apn": "internet.provider.com",
					},
				},
			},
		},
		{
			name: "handle keys with multiple dots",
			input: map[string]string{
				"scooter.nested.setting": "value",
			},
			expected: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"nested.setting": "value",
					},
				},
			},
		},
		{
			name:  "handle empty input",
			input: map[string]string{},
			expected: &Config{
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
			name: "convert standard sections",
			config: &Config{
				Sections: map[string]map[string]interface{}{
					"scooter": {
						"speed_limit": "25",
						"mode":        "eco",
					},
					"cellular": {
						"apn": "internet.provider.com",
					},
				},
			},
			expected: map[string]interface{}{
				"scooter.speed_limit": "25",
				"scooter.mode":        "eco",
				"cellular.apn":        "internet.provider.com",
			},
		},
		{
			name: "convert custom sections",
			config: &Config{
				Sections: map[string]map[string]interface{}{
					"custom": {
						"setting1": "value1",
					},
					"another": {
						"setting2": "value2",
					},
				},
			},
			expected: map[string]interface{}{
				"custom.setting1":  "value1",
				"another.setting2": "value2",
			},
		},
		{
			name: "handle empty config",
			config: &Config{
				Sections: map[string]map[string]interface{}{},
			},
			expected: map[string]interface{}{},
		},
		{
			name: "handle nested dots in keys",
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
	// Test that parsing and converting back produces the same result
	input := map[string]string{
		"scooter.speed_limit": "25",
		"scooter.mode":        "eco",
		"cellular.apn":        "internet.provider.com",
		"custom.setting":      "custom_value",
	}

	config := ParseRedisSettings(input)
	output := config.ToRedisFields()

	// Convert output back to map[string]string for comparison
	outputStr := make(map[string]string)
	for k, v := range output {
		outputStr[k] = v.(string)
	}

	if !reflect.DeepEqual(input, outputStr) {
		t.Errorf("Round-trip conversion failed. Input: %v, Output: %v", input, outputStr)
	}
}
