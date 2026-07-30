package utils

import (
	"errors"
	"regexp"
)

// ValidateEmail returns an error if email is not a valid email address.
func ValidateEmail(email string) error {
	const emailRegex = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	if matched, _ := regexp.MatchString(emailRegex, email); !matched {
		return errors.New("invalid email format")
	}
	return nil
}

// ValidateInput checks if the input string is not empty.
func ValidateInput(input string) error {
	if input == "" {
		return errors.New("input cannot be empty")
	}
	return nil
}
