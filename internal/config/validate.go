package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("yaml"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
}

// ValidationError represents a single field validation error.
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationErrors is a slice of ValidationError that serializes to JSON.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Validate checks the configuration struct recursively according to validate tags.
func (f *File) Validate() error {
	if f == nil {
		return errors.New("config is nil")
	}
	err := validate.Struct(f)
	var out ValidationErrors
	if errs, ok := err.(validator.ValidationErrors); ok {
		for _, e := range errs {
			ns := e.Namespace()
			// Strip the root struct name (e.g., "File.")
			if idx := strings.Index(ns, "."); idx != -1 {
				ns = ns[idx+1:]
			}

			// Build a friendly message
			var msg string
			switch e.Tag() {
			case "required":
				msg = "This field is required."
			case "required_without":
				msg = "This field is required when " + e.Param() + " is empty."
			case "ip":
				msg = "Must be a valid IP address."
			case "url":
				msg = "Must be a valid URL."
			default:
				msg = "Validation failed on tag: " + e.Tag()
			}

			out = append(out, ValidationError{
				Path:    ns,
				Message: msg,
			})
		}
	} else if err != nil {
		return err
	}

	for name, app := range f.Apps {
		if err := app.Validate(); err != nil {
			out = append(out, ValidationError{
				Path:    "apps." + name,
				Message: err.Error(),
			})
		}
	}

	if len(out) > 0 {
		return out
	}
	return nil
}
