package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
)

// Validate checks the configuration struct recursively according to honey tags.
func (f *File) Validate() error {
	if f == nil {
		return errors.New("config is nil")
	}

	val := reflect.ValueOf(*f)
	return validateStruct(val, "config")
}

func validateStruct(val reflect.Value, path string) error {
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil
	}

	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldVal := val.Field(i)
		
		yamlTag := yamlTagName(field)
		if yamlTag == "" {
			continue
		}

		currentPath := path + "." + yamlTag
		if path == "config" {
			currentPath = yamlTag
		}

		opts := parseHoneyTag(field.Tag.Get("honey"))
		
		if err := validateField(fieldVal, currentPath, opts); err != nil {
			return err
		}

		if fieldVal.Kind() == reflect.Struct {
			if err := validateStruct(fieldVal, currentPath); err != nil {
				return err
			}
		}

		if fieldVal.Kind() == reflect.Slice {
			for j := 0; j < fieldVal.Len(); j++ {
				elem := fieldVal.Index(j)
				elemPath := fmt.Sprintf("%s[%d]", currentPath, j)
				
				if elem.Kind() == reflect.Struct {
					if err := validateStruct(elem, elemPath); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func validateField(val reflect.Value, path string, opts map[string]string) error {
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			val = reflect.Value{}
		} else {
			val = val.Elem()
		}
	}

	isEmpty := false
	if !val.IsValid() || val.IsZero() {
		isEmpty = true
	} else if val.Kind() == reflect.String && strings.TrimSpace(val.String()) == "" {
		isEmpty = true
	} else if val.Kind() == reflect.Slice && val.Len() == 0 {
		isEmpty = true
	}

	isRequired := hasHoneyFlag(opts, "required")
	if isRequired && isEmpty {
		return fmt.Errorf("%s is required", path)
	}

	if isEmpty {
		return nil
	}

	if val.Kind() == reflect.String {
		strVal := strings.TrimSpace(val.String())
		
		if rawEnum, ok := opts["enum"]; ok && rawEnum != "" {
			parts := strings.Split(rawEnum, "|")
			isValidEnum := false
			for _, p := range parts {
				if strings.TrimSpace(p) == strVal {
					isValidEnum = true
					break
				}
			}
			isWarning := hasHoneyFlag(opts, "enum_as_warning")
			if !isValidEnum && !isWarning {
				return fmt.Errorf("%s must be one of [%s]", path, strings.Join(parts, ", "))
			}
		}

		if format, ok := opts["format"]; ok {
			switch format {
			case "ip":
				if net.ParseIP(strVal) == nil {
					return fmt.Errorf("%s must be a valid IP address", path)
				}
			case "url":
				if _, err := url.ParseRequestURI(strVal); err != nil {
					return fmt.Errorf("%s must be a valid URL", path)
				}
			}
		}
	}

	return nil
}
