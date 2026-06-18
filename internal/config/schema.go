package config

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/apps"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// SchemaFieldType describes supported primitive config field kinds.
type SchemaFieldType string

// Primitive field kinds for defaults/backends schema and JSON Schema "type".
const (
	SchemaFieldTypeString  SchemaFieldType = "string"
	SchemaFieldTypeBoolean SchemaFieldType = "boolean"
	SchemaFieldTypeInteger SchemaFieldType = "integer"
	SchemaFieldTypeNumber  SchemaFieldType = "number"
	SchemaFieldTypeArray   SchemaFieldType = "array"
	SchemaFieldTypeObject  SchemaFieldType = "object"
)

// SchemaField describes one editable key in defaults/backends schema.
type SchemaField struct {
	Key           string          `json:"key"`
	Label         string          `json:"label"`
	Type          SchemaFieldType `json:"type"`
	Format        string          `json:"format,omitempty"` // "ip", "url", etc.
	Required      bool            `json:"required,omitempty"`
	Secret        bool            `json:"secret,omitempty"`
	Enum          []string        `json:"enum,omitempty"`
	EnumAsWarning bool            `json:"enum_as_warning,omitempty"`
	Default       any             `json:"default,omitempty"`
	Items         []SchemaField   `json:"items,omitempty"` // For nested array of objects
}

// BackendSchema describes one backend kind and its field layout.
type BackendSchema struct {
	Label  string        `json:"label"`
	Fields []SchemaField `json:"fields"`
}

// UISchema is the lightweight UI-focused schema payload.
type UISchema struct {
	TopLevelKeys []string                 `json:"top_level_keys"`
	Defaults     []SchemaField            `json:"defaults"`
	Backends     map[string]BackendSchema `json:"backends"`
	BackendOrder []string                 `json:"backend_order"`
}

// BuildUISchema returns the lightweight schema used by web UI rendering and linting.
func BuildUISchema() UISchema {
	defaults := schemaFieldsFromStruct(reflect.TypeOf(Defaults{}))
	backends, backendOrder := backendSchemasFromStruct(reflect.TypeOf(Backends{}))

	return UISchema{
		TopLevelKeys: topLevelKeysFromFileType(reflect.TypeOf(File{})),
		Defaults:     defaults,
		Backends:     backends,
		BackendOrder: backendOrder,
	}
}

// BuildJSONSchema returns a JSON Schema payload generated from the UI schema.
func BuildJSONSchema() map[string]any {
	ui := BuildUISchema()

	defaultProps := map[string]any{}
	for _, f := range ui.Defaults {
		defaultProps[f.Key] = jsonSchemaField(f)
	}

	backendProps := map[string]any{}
	for kind, b := range ui.Backends {
		itemProps := map[string]any{}
		for _, f := range b.Fields {
			itemProps[f.Key] = jsonSchemaField(f)
		}
		backendProps[kind] = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"properties":           itemProps,
				"additionalProperties": false,
			},
		}
	}

	appFields := schemaFieldsFromStruct(reflect.TypeOf(apps.AppConfig{}))
	appProps := map[string]any{}
	for _, f := range appFields {
		appProps[f.Key] = jsonSchemaField(f)
	}
	appProps["type"] = map[string]any{
		"type": "string",
		"enum": []string{"http", "tcp", "recipe"},
	}

	rootProps := map[string]any{
		"version": map[string]any{
			"type": "integer",
		},
		"defaults": map[string]any{
			"type":                 "object",
			"properties":           defaultProps,
			"additionalProperties": false,
		},
		"backends": map[string]any{
			"type":                 "object",
			"properties":           backendProps,
			"additionalProperties": false,
		},
		"apps": map[string]any{
			"type": "object",
			"patternProperties": map[string]any{
				"^[a-zA-Z0-9_-]+$": map[string]any{
					"type":                 "object",
					"properties":           appProps,
					"additionalProperties": false,
				},
			},
			"additionalProperties": false,
		},
	}

	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           rootProps,
		"additionalProperties": false,
	}
}

func jsonSchemaField(f SchemaField) map[string]any {
	out := map[string]any{
		"type": string(f.Type),
	}
	if f.Type == SchemaFieldTypeObject {
		itemProps := map[string]any{}
		for _, item := range f.Items {
			itemProps[item.Key] = jsonSchemaField(item)
		}
		out["properties"] = itemProps
		out["additionalProperties"] = false
	} else if f.Type == SchemaFieldTypeArray && len(f.Items) > 0 {
		// Differentiate between array of objects and array of primitive types
		if f.Items[0].Key == "" && f.Items[0].Type == SchemaFieldTypeString {
			out["items"] = map[string]any{
				"type": "string",
			}
		} else {
			itemProps := map[string]any{}
			for _, item := range f.Items {
				itemProps[item.Key] = jsonSchemaField(item)
			}
			out["items"] = map[string]any{
				"type":                 "object",
				"properties":           itemProps,
				"additionalProperties": false,
			}
		}
	}
	if len(f.Enum) > 0 {
		vals := make([]any, 0, len(f.Enum))
		for _, v := range f.Enum {
			vals = append(vals, strings.TrimSpace(v))
		}
		out["enum"] = vals
	}
	if f.Default != nil {
		out["default"] = f.Default
	}
	return out
}

func topLevelKeysFromFileType(t reflect.Type) []string {
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := yamlTagName(f)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

type backendFieldOrder struct {
	kind  string
	order int
}

func backendSchemasFromStruct(t reflect.Type) (map[string]BackendSchema, []string) {
	out := map[string]BackendSchema{}
	order := make([]backendFieldOrder, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		kind := yamlTagName(f)
		if kind == "" || f.Type.Kind() != reflect.Slice {
			continue
		}
		opts := parseHoneyTag(f.Tag.Get("honey"))
		label := opts["label"]
		if strings.TrimSpace(label) == "" {
			label = cases.Title(language.English).String(kind)
		}
		out[kind] = BackendSchema{
			Label:  label,
			Fields: schemaFieldsFromStruct(f.Type.Elem()),
		}
		pos := parseIntOrDefault(opts["order"], (i+1)*10)
		order = append(order, backendFieldOrder{kind: kind, order: pos})
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].order == order[j].order {
			return order[i].kind < order[j].kind
		}
		return order[i].order < order[j].order
	})
	backendOrder := make([]string, 0, len(order))
	for _, item := range order {
		backendOrder = append(backendOrder, item.kind)
	}
	return out, backendOrder
}

func schemaFieldsFromStruct(t reflect.Type) []SchemaField {
	fields := make([]SchemaField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := yamlTagName(f)
		if key == "" {
			continue
		}

		vTag := f.Tag.Get("validate")

		var fieldType SchemaFieldType
		var items []SchemaField

		k := f.Type.Kind()
		switch k {
		case reflect.Pointer:
			ft, ok := schemaTypeForGoType(f.Type.Elem())
			if !ok {
				continue
			}
			fieldType = ft
		case reflect.Struct:
			fieldType = SchemaFieldTypeObject
			items = schemaFieldsFromStruct(f.Type)
		case reflect.Slice:
			// Basic support for slice of structs (or slice of basic types if we ever need it)
			fieldType = SchemaFieldTypeArray
			if f.Type.Elem().Kind() == reflect.Struct {
				items = schemaFieldsFromStruct(f.Type.Elem())
			} else if f.Type.Elem().Kind() == reflect.String {
				// E.g. []string
				var itemFormat string
				if strings.Contains(vTag, "dive,ip") {
					itemFormat = "ip"
				} else if strings.Contains(vTag, "dive,url") {
					itemFormat = "url"
				}
				items = []SchemaField{{Type: SchemaFieldTypeString, Format: itemFormat}}
			}
		case reflect.Map:
			// For map[string]string (like Meta), we handle it differently below
			// The json schema builder doesn't strictly support free-form objects out of the box in the same way,
			// but we can map it to an object type if needed.
			continue // Skip maps in the UI schema for now since there's no UI for arbitrary k/v
		default:
			var ok bool
			fieldType, ok = schemaTypeForGoType(f.Type)
			if !ok {
				continue
			}
		}

		opts := parseHoneyTag(f.Tag.Get("honey"))

		var format string
		if strings.Contains(vTag, "ip") && !strings.Contains(vTag, "dive,ip") {
			format = "ip"
		} else if strings.Contains(vTag, "url") && !strings.Contains(vTag, "dive,url") {
			format = "url"
		}

		isRequired := strings.Contains(vTag, "required") && !strings.Contains(vTag, "required_without")

		sf := SchemaField{
			Key:      key,
			Label:    firstNonEmpty(opts["label"], key),
			Type:     fieldType,
			Format:   format,
			Required: isRequired,
			Secret:   hasHoneyFlag(opts, "secret"),
			Items:    items,
		}
		if rawEnum := strings.TrimSpace(opts["enum"]); rawEnum != "" {
			parts := strings.Split(rawEnum, "|")
			sf.Enum = make([]string, 0, len(parts))
			for _, p := range parts {
				s := strings.TrimSpace(p)
				if s != "" {
					sf.Enum = append(sf.Enum, s)
				}
			}
		}
		sf.EnumAsWarning = hasHoneyFlag(opts, "enum_as_warning")
		if rawDefault, ok := opts["default"]; ok {
			if v, ok := parseDefaultValue(rawDefault, fieldType); ok {
				sf.Default = v
			}
		}
		fields = append(fields, sf)
	}
	return fields
}

func schemaTypeForGoType(t reflect.Type) (SchemaFieldType, bool) {
	switch t.Kind() {
	case reflect.String:
		return SchemaFieldTypeString, true
	case reflect.Bool:
		return SchemaFieldTypeBoolean, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return SchemaFieldTypeInteger, true
	case reflect.Float32, reflect.Float64:
		return SchemaFieldTypeNumber, true
	default:
		return "", false
	}
}

func yamlTagName(f reflect.StructField) string {
	tag := strings.TrimSpace(f.Tag.Get("yaml"))
	if tag == "" || tag == "-" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	return strings.TrimSpace(name)
}

func parseHoneyTag(tag string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(tag, ";") {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if eq := strings.Index(part, "="); eq >= 0 {
			key := strings.TrimSpace(part[:eq])
			val := strings.TrimSpace(part[eq+1:])
			if key != "" {
				out[key] = val
			}
			continue
		}
		out[part] = "true"
	}
	return out
}

func hasHoneyFlag(m map[string]string, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(v), "false") {
		return false
	}
	return true
}

func parseDefaultValue(raw string, fieldType SchemaFieldType) (any, bool) {
	raw = strings.TrimSpace(raw)
	switch fieldType {
	case SchemaFieldTypeString:
		return raw, true
	case SchemaFieldTypeBoolean:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, false
		}
		return v, true
	case SchemaFieldTypeInteger:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, false
		}
		return v, true
	case SchemaFieldTypeNumber:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, false
		}
		return v, true
	default:
		return nil, false
	}
}

func parseIntOrDefault(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return v
}

func firstNonEmpty(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
