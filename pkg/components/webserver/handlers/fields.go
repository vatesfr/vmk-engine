package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type jsonFieldPath []string

func selectCollectionFields[T any](items []T, rawFields string) ([]T, error) {
	collection := reflect.ValueOf(items)
	if collection.Kind() != reflect.Slice && collection.Kind() != reflect.Array {
		return nil, fmt.Errorf("fields can only be applied to a collection")
	}

	fields := strings.TrimSpace(rawFields)
	// If fields is "*" or "", return the entire collection as a slice of any.
	if fields == "*" || fields == "" {
		result := make([]T, collection.Len())
		for index := range collection.Len() {
			result[index] = collection.Index(index).Interface().(T)
		}
		return result, nil
	}

	paths := parseJSONFieldPaths(fields)

	result := make([]T, collection.Len())
	for index := range collection.Len() {
		item := collection.Index(index).Interface().(T)
		projected, err := projectJSONFields(item, paths)
		if err != nil {
			return nil, fmt.Errorf("project response fields: %w", err)
		}
		result[index] = projected
	}
	return result, nil
}

func parseJSONFieldPaths(fields string) []jsonFieldPath {
	parts := strings.Split(fields, ",")
	paths := make([]jsonFieldPath, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" || field == "*" {
			continue // Skip empty or wildcard fields
		}
		// Don't check for valid JSON field names here,
		// if the field is invalid, it will be ignored in the projection.
		segments := strings.Split(field, ".")
		paths = append(paths, segments)
	}
	return paths
}

func projectJSONFields[T any](value T, paths []jsonFieldPath) (T, error) {
	var zero T

	encoded, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return zero, err
	}
	projected, err := json.Marshal(projectJSONValue(decoded, paths))
	if err != nil {
		return zero, err
	}

	var result T
	if err := json.Unmarshal(projected, &result); err != nil {
		return zero, err
	}
	return result, nil
}

func projectJSONValue(value any, paths []jsonFieldPath) any {
	switch value := value.(type) {
	case map[string]any:
		groups := groupJSONFieldPaths(paths)
		projected := make(map[string]any, len(groups))
		for name, tails := range groups {
			fieldValue, present := value[name]
			if !present {
				continue
			}
			if includesWholeField(tails) {
				projected[name] = fieldValue
				continue
			}
			projected[name] = projectJSONValue(fieldValue, tails)
		}
		return projected
	case []any:
		projected := make([]any, len(value))
		for index, item := range value {
			projected[index] = projectJSONValue(item, paths)
		}
		return projected
	default:
		return value
	}
}

func groupJSONFieldPaths(paths []jsonFieldPath) map[string][]jsonFieldPath {
	groups := make(map[string][]jsonFieldPath, len(paths))
	for _, path := range paths {
		groups[path[0]] = append(groups[path[0]], path[1:])
	}
	return groups
}

func includesWholeField(paths []jsonFieldPath) bool {
	for _, path := range paths {
		if len(path) == 0 {
			return true
		}
	}
	return false
}
