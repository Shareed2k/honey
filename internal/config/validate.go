package config

import (
	"errors"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// Validate checks the configuration struct recursively according to validate tags.
func (f *File) Validate() error {
	if f == nil {
		return errors.New("config is nil")
	}
	return validate.Struct(f)
}
