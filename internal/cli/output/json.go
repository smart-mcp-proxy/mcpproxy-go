package output

import (
	"encoding/json"
)

// JSONFormatter formats output as JSON.
type JSONFormatter struct {
	Indent bool // Whether to pretty-print with indentation
}

// Format marshals data to JSON.
func (f *JSONFormatter) Format(data interface{}) (string, error) {
	var output []byte
	var err error
	if f.Indent {
		output, err = json.MarshalIndent(data, "", "  ")
	} else {
		output, err = json.Marshal(data)
	}
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// FormatError marshals a structured error to JSON.
func (f *JSONFormatter) FormatError(err StructuredError) (string, error) {
	return f.Format(err)
}

// FormatTable converts tabular data to a JSON array of objects.
func (f *JSONFormatter) FormatTable(headers []string, rows [][]string) (string, error) {
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
