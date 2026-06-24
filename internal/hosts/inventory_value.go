package hosts

import (
	"encoding/json"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// InventoryValue is a scalar inventory variable value.
type InventoryValue struct {
	value any
}

// NewInventoryValue returns a scalar inventory value or an error for maps,
// lists, nil, or unsupported types.
func NewInventoryValue(v any) (InventoryValue, error) {
	switch x := v.(type) {
	case string:
		return InventoryValue{value: x}, nil
	case bool:
		return InventoryValue{value: x}, nil
	case int:
		return InventoryValue{value: int64(x)}, nil
	case int8:
		return InventoryValue{value: int64(x)}, nil
	case int16:
		return InventoryValue{value: int64(x)}, nil
	case int32:
		return InventoryValue{value: int64(x)}, nil
	case int64:
		return InventoryValue{value: x}, nil
	case uint:
		return InventoryValue{value: uint64(x)}, nil
	case uint8:
		return InventoryValue{value: uint64(x)}, nil
	case uint16:
		return InventoryValue{value: uint64(x)}, nil
	case uint32:
		return InventoryValue{value: uint64(x)}, nil
	case uint64:
		return InventoryValue{value: x}, nil
	case float32:
		return InventoryValue{value: float64(x)}, nil
	case float64:
		return InventoryValue{value: x}, nil
	default:
		return InventoryValue{}, fmt.Errorf("inventory vars only support scalar string, bool, and number values, got %T", v)
	}
}

// MustInventoryValue is for tests and static literals.
func MustInventoryValue(v any) InventoryValue {
	out, err := NewInventoryValue(v)
	if err != nil {
		panic(err)
	}
	return out
}

// UnmarshalYAML rejects non-scalar YAML inventory variable values.
func (v *InventoryValue) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return fmt.Errorf("inventory vars only support scalar string, bool, and number values")
	}
	switch node.Tag {
	case "!!str":
		v.value = node.Value
	case "!!bool":
		b, err := strconv.ParseBool(node.Value)
		if err != nil {
			return err
		}
		v.value = b
	case "!!int":
		i, err := strconv.ParseInt(node.Value, 0, 64)
		if err != nil {
			return err
		}
		v.value = i
	case "!!float":
		f, err := strconv.ParseFloat(node.Value, 64)
		if err != nil {
			return err
		}
		v.value = f
	default:
		return fmt.Errorf("inventory vars only support scalar string, bool, and number values")
	}
	return nil
}

// MarshalJSON writes the scalar value for host JSON output.
func (v InventoryValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.value)
}

// Any returns the underlying scalar value for CEL and JSON contexts.
func (v InventoryValue) Any() any {
	return v.value
}

// IsSet reports whether the value was decoded from a supported scalar.
func (v InventoryValue) IsSet() bool {
	return v.value != nil
}

// Bool returns the underlying bool or false for non-bool values.
func (v InventoryValue) Bool() bool {
	b, _ := v.value.(bool)
	return b
}

// String returns a stable string form for env export.
func (v InventoryValue) String() string {
	switch x := v.value.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}
