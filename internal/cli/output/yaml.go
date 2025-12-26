package output

import (
	"gopkg.in/yaml.v3"
)

// YAMLFormatter formats output as YAML.
type YAMLFormatter struct{}

// Format marshals data to YAML.
func (f *YAMLFormatter) Format(data interface{}) (string, error) {
	output, err := yaml.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// FormatError marshals a structured error to YAML.
func (f *YAMLFormatter) FormatError(err StructuredError) (string, error) {
	return f.Format(err)
}

// FormatTable converts tabular data to YAML.
func (f *YAMLFormatter) FormatTable(headers []string, rows [][]string) (string, error) {
	// Convert table to slice of maps
	result := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]string)
		for i, header := range headers {
			if i < len(row) {
				obj[header] = row[i]
			} else {
				obj[header] = ""
			}
		}
		result = append(result, obj)
	}
	return f.Format(result)
}
